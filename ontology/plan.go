package ontology

import "fmt"

// 本文件实现写入计划（WritePlan）与计划构建器（PlanBuilder）。
//
// 写入计划以“对象 ID → 最终写入”的映射保存：同一次调用中，
// 后续步骤对同一对象的写入会覆盖先前步骤的写入，因此后置校验
// 看到的永远是最终计划而非任何中间状态。
//
// 计划在提交前完全是内存中的私有结构，对已提交状态零影响；
// 后置校验失败时整体丢弃即可，效果与从未尝试完全等价。

// writeKind 是单条写入的种类。
type writeKind int

const (
	writeCreate writeKind = iota
	writeUpdate
	writeDelete
)

// objectWrite 是对一个对象的最终写入。
type objectWrite struct {
	kind    writeKind
	objType string
	props   map[string]any
}

// linkWrite 是对一条链接的最终写入。
type linkWrite struct {
	kind     writeKind
	linkType string
	from, to ObjectID
}

// WritePlan 是一次执行计划写入的最终结果。
type WritePlan struct {
	objects map[ObjectID]objectWrite
	links   map[LinkID]linkWrite
}

func newWritePlan() *WritePlan {
	return &WritePlan{
		objects: make(map[ObjectID]objectWrite),
		links:   make(map[LinkID]linkWrite),
	}
}

// Empty 报告计划是否不包含任何写入。
func (p *WritePlan) Empty() bool { return len(p.objects) == 0 && len(p.links) == 0 }

// ApplyContext 是动作 Apply 函数的上下文：
// 只能读取已提交快照（State），只能写入写入计划（Plan）。
type ApplyContext struct {
	Params map[string]any
	State  *StateView
	Plan   *PlanBuilder
}

// PlanBuilder 在快照视图之上累积写入计划。
//
// 所有写操作都会先在视图中登记读集（创建登记“目标不存在”、
// 更新/删除登记目标当前版本），保证提交时的乐观并发校验覆盖
// 写入所依赖的全部前提。
type PlanBuilder struct {
	view *StateView
	plan *WritePlan
}

func newPlanBuilder(view *StateView) *PlanBuilder {
	return &PlanBuilder{view: view, plan: newWritePlan()}
}

// Create 计划创建一个新对象；对象在快照中必须不存在。
func (b *PlanBuilder) Create(id ObjectID, objType string, props map[string]any) error {
	if _, exists := b.view.GetObject(id); exists {
		return fmt.Errorf("ontology: cannot create object %q: already exists in committed snapshot", id)
	}
	b.plan.objects[id] = objectWrite{kind: writeCreate, objType: objType, props: copyProps(props)}
	return nil
}

// Update 计划更新一个已存在对象的属性（整体替换 Props）。
// 若同一对象此前已有计划写入，本调用覆盖之（最终计划语义）。
func (b *PlanBuilder) Update(id ObjectID, props map[string]any) error {
	obj, exists := b.view.GetObject(id)
	if !exists {
		return fmt.Errorf("ontology: cannot update object %q: not found in committed snapshot", id)
	}
	b.plan.objects[id] = objectWrite{kind: writeUpdate, objType: obj.Type, props: copyProps(props)}
	return nil
}

// Delete 计划删除一个已存在的对象。
func (b *PlanBuilder) Delete(id ObjectID) error {
	if _, exists := b.view.GetObject(id); !exists {
		return fmt.Errorf("ontology: cannot delete object %q: not found in committed snapshot", id)
	}
	b.plan.objects[id] = objectWrite{kind: writeDelete}
	return nil
}

// PutLink 计划创建一条链接；链接 ID 在快照中必须不存在。
func (b *PlanBuilder) PutLink(id LinkID, linkType string, from, to ObjectID) error {
	if _, exists := b.view.GetLink(id); exists {
		return fmt.Errorf("ontology: cannot create link %q: already exists in committed snapshot", id)
	}
	b.plan.links[id] = linkWrite{kind: writeCreate, linkType: linkType, from: from, to: to}
	return nil
}

// DeleteLink 计划删除一条已存在的链接。
func (b *PlanBuilder) DeleteLink(id LinkID) error {
	if _, exists := b.view.GetLink(id); !exists {
		return fmt.Errorf("ontology: cannot delete link %q: not found in committed snapshot", id)
	}
	b.plan.links[id] = linkWrite{kind: writeDelete}
	return nil
}

// PlannedObject 返回某对象当前的计划写入（供 Apply 内部自查，不影响最终计划）。
func (b *PlanBuilder) PlannedObject(id ObjectID) (kind string, ok bool) {
	w, exists := b.plan.objects[id]
	if !exists {
		return "", false
	}
	switch w.kind {
	case writeCreate:
		return "create", true
	case writeUpdate:
		return "update", true
	}
	return "delete", true
}

func copyProps(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}
