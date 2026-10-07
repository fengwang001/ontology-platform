package ontology

import "fmt"

// 本文件是一个独立的朴素串行参照实现，用于并发正确性测试：
// 并发引擎的被接受调用集合与效果，必须等价于按某个全序（提交序号）
// 在本实现上逐一执行的结果。
//
// 本实现刻意保持简单：无锁、无快照、无并发控制，直接在自身状态上
// 顺序评估并应用。它与并发引擎共享条件/动作类型定义，但状态管理与
// 提交流程完全独立编写，避免“用被测系统验证被测系统”。

// NaiveExecutor 是单线程串行参照执行器（非并发安全）。
type NaiveExecutor struct {
	objects  map[ObjectID]Object
	links    map[LinkID]Link
	registry *Registry

	commitSeq     int64
	objectHistory map[ObjectID][]HistoryEntry
	actionHistory map[string][]HistoryEntry
}

// NewNaiveExecutor 创建串行参照执行器。
func NewNaiveExecutor(registry *Registry) *NaiveExecutor {
	return &NaiveExecutor{
		objects:       make(map[ObjectID]Object),
		links:         make(map[LinkID]Link),
		registry:      registry,
		objectHistory: make(map[ObjectID][]HistoryEntry),
		actionHistory: make(map[string][]HistoryEntry),
	}
}

// Seed 写入初始对象（版本 0）。
func (n *NaiveExecutor) Seed(obj Object) {
	obj.Props = copyProps(obj.Props)
	obj.Version = 0
	n.objects[obj.ID] = obj
}

// Execute 串行执行一次调用，语义与并发引擎一致：
// 前置全评估 → 计划 → 后置首个失败 → 直接应用。
func (n *NaiveExecutor) Execute(call Call) Result {
	at, ok := n.registry.Get(call.ActionType)
	if !ok {
		return Result{CallID: call.ID, Status: StatusError,
			Err: fmt.Errorf("ontology: unknown action type %q", call.ActionType)}
	}
	if c := at.analysis.Contradiction; c != nil {
		return Result{CallID: call.ID, Status: StatusRejected, Reject: &Rejection{
			Category: RejectDeclarationContradiction, Detail: c.Error()}}
	}
	// 以当前状态构造快照与视图，复用同一套条件求值入口。
	snap := &snapshot{
		objects: make(map[ObjectID]Object, len(n.objects)),
		links:   make(map[LinkID]Link, len(n.links)),
	}
	for id, o := range n.objects {
		snap.objects[id] = Object{ID: o.ID, Type: o.Type, Props: copyProps(o.Props), Version: o.Version}
	}
	for id, l := range n.links {
		snap.links[id] = l
	}
	view := newStateView(snap)
	for _, t := range call.Targets {
		if _, exists := view.GetObject(t); !exists {
			return Result{CallID: call.ID, Status: StatusRejected, Reject: &Rejection{
				Category: RejectConcurrentInvalidation,
				Detail:   fmt.Sprintf("target object %q is not present in committed state", t)}}
		}
	}
	var failedPre []string
	for _, c := range at.Preconditions {
		if !c.Eval(PreInput{Params: call.Params, State: view}) {
			failedPre = append(failedPre, c.ID)
		}
	}
	if len(failedPre) > 0 {
		return Result{CallID: call.ID, Status: StatusRejected, Reject: &Rejection{
			Category: RejectPrecondition, FailedPreconditions: failedPre}}
	}
	builder := newPlanBuilder(view)
	if at.Apply != nil {
		if err := at.Apply(&ApplyContext{Params: call.Params, State: view, Plan: builder}); err != nil {
			return Result{CallID: call.ID, Status: StatusError, Err: err}
		}
	}
	planned := newPlannedView(snap, builder.plan)
	for _, c := range at.Postconditions {
		if !c.Eval(PostInput{Params: call.Params, State: planned}) {
			return Result{CallID: call.ID, Status: StatusRejected, Reject: &Rejection{
				Category: RejectPostcondition, DecisivePostcondition: c.ID}}
		}
	}
	// 直接应用计划（无并发校验）。
	for id, w := range builder.plan.objects {
		switch w.kind {
		case writeCreate:
			n.objects[id] = Object{ID: id, Type: w.objType, Props: copyProps(w.props), Version: 1}
		case writeUpdate:
			cur := n.objects[id]
			cur.Props = copyProps(w.props)
			cur.Version++
			n.objects[id] = cur
		case writeDelete:
			delete(n.objects, id)
		}
	}
	for id, w := range builder.plan.links {
		if w.kind == writeCreate {
			n.links[id] = Link{ID: id, Type: w.linkType, From: w.from, To: w.to, Version: 1}
		} else {
			delete(n.links, id)
		}
	}
	n.commitSeq++
	for id := range builder.plan.objects {
		var version int64
		if cur, ok := n.objects[id]; ok {
			version = cur.Version
		}
		n.objectHistory[id] = append(n.objectHistory[id], HistoryEntry{
			Seq: int64(len(n.objectHistory[id]) + 1), CallID: call.ID,
			ActionType: at.ID, Version: version,
		})
	}
	n.actionHistory[at.ID] = append(n.actionHistory[at.ID], HistoryEntry{
		Seq: int64(len(n.actionHistory[at.ID]) + 1), CallID: call.ID,
	})
	return Result{CallID: call.ID, Status: StatusAccepted, CommitSeq: n.commitSeq}
}

// GetObject 返回当前对象状态。
func (n *NaiveExecutor) GetObject(id ObjectID) (Object, bool) {
	o, ok := n.objects[id]
	return o, ok
}

// ObjectIDs 返回全部现存对象 ID。
func (n *NaiveExecutor) ObjectIDs() []ObjectID {
	ids := make([]ObjectID, 0, len(n.objects))
	for id := range n.objects {
		ids = append(ids, id)
	}
	return ids
}

// ObjectHistory 返回对象历史。
func (n *NaiveExecutor) ObjectHistory(id ObjectID) []HistoryEntry {
	return append([]HistoryEntry(nil), n.objectHistory[id]...)
}

// ActionHistory 返回动作类型历史。
func (n *NaiveExecutor) ActionHistory(actionType string) []HistoryEntry {
	return append([]HistoryEntry(nil), n.actionHistory[actionType]...)
}
