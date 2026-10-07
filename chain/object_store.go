// Package chain 实现本体平台中动作（Action）的嵌套调用语义：
// 前置条件的继承边界、写入计划的中间可见性、后置效果传播、
// 非关键调用、自我触发检测以及并发链条的可串行化。
package chain

import "sync"

// Object 是本体对象的最小模型：主键 + 属性表。
type Object struct {
	ID         string
	Attributes map[string]any
}

// WriteOp 表示一次写入计划中的单条操作。
// 创建/更新都由 Upsert 表达；删除由 Delete 表达。
type WriteOp struct {
	ObjectID string
	Delete   bool
	Upsert   map[string]any // Delete 为 false 时生效；与既有属性合并，键值覆盖
}

// ObjectStore 是持久化对象存储。执行期间所有写入只存在于 WritePlan 中，
// 只有在整条调用链条成功结束后才由提交点一次性提交。
type ObjectStore struct {
	mu      sync.Mutex
	objects map[string]map[string]any
}

// NewObjectStore 创建空存储。
func NewObjectStore() *ObjectStore {
	return &ObjectStore{objects: make(map[string]map[string]any)}
}

// Snapshot 返回持久化状态的深拷贝，供链条重试验证使用。
func (s *ObjectStore) Snapshot() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.objects)
}

// Commit 按顺序提交一条写入计划。返回提交后的状态快照（深拷贝）。
func (s *ObjectStore) Commit(plan []WriteOp) map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	applyPlan(s.objects, plan)
	return cloneState(s.objects)
}

// View 是“持久化状态 + 若干层未提交写入计划”叠加后的只读视图。
// 内层动作的前置条件看到的是外层中间写入计划；单个动作自身的
// 前置条件只能看到其进入时视图中的状态（持久化 + 外层计划），
// 看不到本动作后续才计算出的写入。
type View struct {
	base     map[string]map[string]any
	overlays [][]WriteOp
}

// NewView 基于持久化快照构造视图。
func NewView(base map[string]map[string]any) *View {
	return &View{base: base}
}

// Push 叠加一层写入计划，返回叠加后的新视图（不修改接收者）。
func (v *View) Push(plan []WriteOp) *View {
	overlays := make([][]WriteOp, len(v.overlays), len(v.overlays)+1)
	copy(overlays, v.overlays)
	overlays = append(overlays, plan)
	return &View{base: v.base, overlays: overlays}
}

// Get 读取对象在叠加视图下的属性；不存在时 ok 为 false。
func (v *View) Get(id string) (attrs map[string]any, ok bool) {
	// 从持久化状态出发，按外层到内层的顺序叠加写入计划。
	base, exists := v.base[id]
	cur := cloneAttrs(base) // 即使不存在也返回空 map，便于叠加
	for _, plan := range v.overlays {
		for _, op := range plan {
			if op.ObjectID != id {
				continue
			}
			if op.Delete {
				cur = nil
				exists = false
				continue
			}
			if cur == nil {
				cur = make(map[string]any)
			}
			for k, val := range op.Upsert {
				cur[k] = val
			}
			exists = true
		}
	}
	if !exists {
		return nil, false
	}
	return cur, true
}

// cloneState 深拷贝整个状态。
func cloneState(s map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(s))
	for id, attrs := range s {
		out[id] = cloneAttrs(attrs)
	}
	return out
}

// cloneAttrs 深拷贝单个对象的属性表。
func cloneAttrs(attrs map[string]any) map[string]any {
	if attrs == nil {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// applyPlan 在目标状态上原地按序应用写入计划（提交时使用）。
func applyPlan(state map[string]map[string]any, plan []WriteOp) {
	for _, op := range plan {
		if op.Delete {
			delete(state, op.ObjectID)
			continue
		}
		attrs, ok := state[op.ObjectID]
		if !ok || attrs == nil {
			attrs = make(map[string]any)
			state[op.ObjectID] = attrs
		}
		for k, v := range op.Upsert {
			attrs[k] = v
		}
	}
}
