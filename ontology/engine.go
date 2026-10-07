package ontology

import (
	"fmt"
	"sync/atomic"
)

// 本文件实现动作执行引擎，严格执行以下阶段顺序：
//
//	0. 声明矛盾检查（最早，优先于其余三类拒绝）
//	1. 目标对象存在性检查（并发撤销类别；与前置失败的相对优先级
//	   由本实现固定为“先检查存在性”，对相同输入稳定）
//	2. 前置校验：只读已提交快照，收集全部不通过条件
//	3. 写入计划生成：只写内存中的计划结构
//	4. 后置校验：基于统一计划视图，首个失败即决定性条件
//	5. 提交：乐观并发校验通过后原子应用
//
// 前置失败与后置失败都不会消耗任何对象版本号或历史序号；
// 后置失败与并发冲突会记入独立的失败轨迹（不影响对象状态）。

// Executor 是动作执行引擎。
type Executor struct {
	store    *Store
	registry *Registry
	callSeq  atomic.Int64
}

// NewExecutor 创建执行引擎。
func NewExecutor(store *Store, registry *Registry) *Executor {
	return &Executor{store: store, registry: registry}
}

// Store 返回引擎底层存储（供审计查询）。
func (e *Executor) Store() *Store { return e.store }

// Execute 执行一次动作调用。
func (e *Executor) Execute(call Call) Result {
	if call.ID == "" {
		call.ID = fmt.Sprintf("call-%d", e.callSeq.Add(1))
	}
	at, ok := e.registry.Get(call.ActionType)
	if !ok {
		return Result{CallID: call.ID, Status: StatusError,
			Err: fmt.Errorf("ontology: unknown action type %q", call.ActionType)}
	}
	// 阶段 0：声明矛盾最早判定，优先于其余三类。
	if c := at.analysis.Contradiction; c != nil {
		return e.reject(call, &Rejection{
			Category: RejectDeclarationContradiction,
			Detail:   c.Error(),
		}, nil, false)
	}
	snap := e.store.takeSnapshot()
	view := newStateView(snap)
	// 阶段 1：目标存在性检查（并发撤销类别，优先级固定在前置校验之前）。
	for _, t := range call.Targets {
		if _, exists := view.GetObject(t); !exists {
			return e.reject(call, &Rejection{
				Category: RejectConcurrentInvalidation,
				Detail:   fmt.Sprintf("target object %q is not present in committed state", t),
			}, nil, true)
		}
	}
	// 阶段 2：前置校验——评估全部前置条件，收集所有不通过项。
	preOutcomes := make([]ConditionOutcome, 0, len(at.Preconditions))
	var failedPre []string
	for _, c := range at.Preconditions {
		passed := c.Eval(PreInput{Params: call.Params, State: view})
		preOutcomes = append(preOutcomes, ConditionOutcome{ID: c.ID, Passed: passed})
		if !passed {
			failedPre = append(failedPre, c.ID)
		}
	}
	preSeq := e.store.appendValidation(ValidationRecord{
		CallID: call.ID, ActionType: at.ID, Phase: PhasePre,
		Params: copyParams(call.Params), Targets: append([]ObjectID(nil), call.Targets...),
		Basis: view.basis(), Outcomes: preOutcomes, PassedAll: len(failedPre) == 0,
	})
	if len(failedPre) > 0 {
		// 前置失败：不产生任何可观察状态变化，不进入失败轨迹
		// （校验记录已留存在审计日志中）。
		return e.reject(call, &Rejection{
			Category:            RejectPrecondition,
			FailedPreconditions: failedPre,
			Detail:              fmt.Sprintf("%d precondition(s) failed", len(failedPre)),
		}, nil, false)
	}
	// 阶段 3：生成写入计划（纯内存结构）。
	builder := newPlanBuilder(view)
	if at.Apply != nil {
		if err := at.Apply(&ApplyContext{Params: call.Params, State: view, Plan: builder}); err != nil {
			return Result{CallID: call.ID, Status: StatusError,
				Err: fmt.Errorf("ontology: action %q apply failed: %w", at.ID, err)}
		}
	}
	// 阶段 4：后置校验——统一计划视图，首个失败即决定性条件，不穷举。
	planned := newPlannedView(snap, builder.plan)
	postOutcomes := make([]ConditionOutcome, 0, len(at.Postconditions))
	var decisive string
	for _, c := range at.Postconditions {
		passed := c.Eval(PostInput{Params: call.Params, State: planned})
		postOutcomes = append(postOutcomes, ConditionOutcome{ID: c.ID, Passed: passed})
		if !passed {
			decisive = c.ID
			break
		}
	}
	postSeq := e.store.appendValidation(ValidationRecord{
		CallID: call.ID, ActionType: at.ID, Phase: PhasePost,
		Params: copyParams(call.Params), Targets: append([]ObjectID(nil), call.Targets...),
		Basis: view.basis(), Outcomes: postOutcomes, PassedAll: decisive == "",
	})
	if decisive != "" {
		// 后置失败：写入计划整体放弃，效果与从未尝试等价；
		// 失败记入独立失败轨迹供审计。
		return e.reject(call, &Rejection{
			Category:              RejectPostcondition,
			DecisivePostcondition: decisive,
			Detail:                fmt.Sprintf("postcondition %q failed; write plan discarded", decisive),
		}, []int64{preSeq, postSeq}, true)
	}
	// 阶段 5：提交（乐观并发校验）。
	res := e.store.commit(view.reads, view.linksRead, view.linkGen, builder.plan, call.ID, at.ID)
	if !res.ok {
		return e.reject(call, &Rejection{
			Category: RejectConcurrentInvalidation,
			Detail:   fmt.Sprintf("object %q was concurrently modified or revoked during execution", res.conflict),
		}, []int64{preSeq, postSeq}, true)
	}
	return Result{CallID: call.ID, Status: StatusAccepted, CommitSeq: res.commitSeq}
}

// reject 构造拒绝结果；recordFailure 为 true 时把失败记入独立失败轨迹。
func (e *Executor) reject(call Call, rej *Rejection, validationSeqs []int64, recordFailure bool) Result {
	if recordFailure {
		e.store.appendFailure(FailureRecord{
			CallID: call.ID, ActionType: call.ActionType,
			Category: rej.Category, ValidationSeqs: validationSeqs, Detail: rej.Detail,
		})
	}
	return Result{CallID: call.ID, Status: StatusRejected, Reject: rej}
}

func copyParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}
