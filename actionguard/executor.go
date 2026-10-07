package actionguard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Executor 以严格串行化方式执行动作调用。
//
// 一次调用的完整阶段顺序为：
//  0. 声明自相矛盾在 Register 时即被拒绝（最早阶段，优先级最高）。
//  1. 进入 Store 临界区，取得执行前深拷贝快照（快照即唯一判断依据）。
//  2. 目标对象并发撤销检查（与前置失败的相对优先级在此固定为
//     “撤销优先”，且对相同输入稳定）。
//  3. 前置校验：只读取执行前快照；全部条件都求值，返回全部不通过条件。
//  4. 计划生成：纯函数，不接触 Store；同对象多次写入在计划中折叠为最终值。
//  5. 后置校验：所有写入计划生成完毕后，以同一个投影快照统一求值；
//     遇第一个不通过条件即作为决定性条件返回（不穷举），计划整体放弃，
//     仅写入独立失败轨迹。
//  6. 提交：版本号/历史只在此刻分配与追加。
//
// 由于每次调用都在同一个 Store 临界区内完成 1–6，调用之间天然构成
// 全序，因此不可能出现两个调用都基于旧快照通过前置、提交后共同违反
// 后置不变量的情况。
type Executor struct {
	store *Store
	defs  map[string]*Action
}

func NewExecutor(store *Store) *Executor {
	return &Executor{store: store, defs: map[string]*Action{}}
}

// Register 注册动作声明。声明自相矛盾在此刻（最早阶段）暴露为错误，
// 该动作不会被注册，更不可能被执行。
func (e *Executor) Register(a *Action) error {
	if err := checkConsistency(a); err != nil {
		return err
	}
	e.defs[a.Type] = a
	return nil
}

// MustRegister 在声明自相矛盾时 panic，用于包初始化期暴露配置错误。
func (e *Executor) MustRegister(a *Action) {
	if err := e.Register(a); err != nil {
		panic(err)
	}
}

func (e *Executor) Execute(ctx context.Context, actionType, callID string, input map[string]any, targetIDs []string) *Outcome {
	a, ok := e.defs[actionType]
	if !ok {
		return &Outcome{Class: OutcomePreRejected, ActionType: actionType, CallID: callID,
			FailedPre: nil, Detail: "unknown action type"}
	}
	inputJSON, _ := json.Marshal(input)

	e.store.Lock()
	defer e.store.Unlock()

	// 1. 执行前唯一快照：前置阶段与计划阶段只共享这一个深拷贝。
	base := e.store.snapshotForExecutor()
	env := buildEnv(input, targetIDs)
	// 自动绑定执行前快照中的全集对象 ID（稳定排序），供“跨全部对象”的
	// 后置不变量（如总量守恒）显式引用；该绑定在整个调用中固定。
	env["allIdsSorted"] = base.allIDsJoined()

	// 2. 并发撤销检查（优先级固定：撤销先于前置失败，相同输入结果稳定）。
	var revoked []string
	for _, id := range targetIDs {
		if base.Revoked(id) {
			revoked = append(revoked, id)
		}
	}
	if len(revoked) > 0 {
		sort.Strings(revoked)
		o := &Outcome{Class: OutcomeObjectRevoked, ActionType: actionType, CallID: callID,
			Detail: "target object revoked: " + strings.Join(revoked, ",")}
		e.store.appendAuditLocked(AuditEntry{CallID: callID, ActionType: actionType,
			Phase: "revocation", Input: string(inputJSON),
			Basis:  "revoked(" + strings.Join(revoked, ",") + ")=true on pre-execution snapshot",
			Result: "fail"})
		return o
	}

	// 3. 前置校验：无条件求值全部前置条件，收集“全部”不通过条件。
	preClauses, err := a.Pre(input)
	if err != nil {
		return &Outcome{Class: OutcomePreRejected, ActionType: actionType, CallID: callID,
			Detail: "precondition construction failed: " + err.Error()}
	}
	var failedPre []string
	var auditRows []AuditEntry
	for _, clause := range preClauses {
		pass, ev := evaluateClause(clause, base, env)
		auditRows = append(auditRows, AuditEntry{CallID: callID, ActionType: actionType,
			Phase: "pre", Condition: clause.Name, Input: string(inputJSON),
			Basis: evidenceBasis(ev), Result: passResult(pass)})
		if !pass {
			failedPre = append(failedPre, clause.Name)
		}
	}
	for _, row := range auditRows {
		e.store.appendAuditLocked(row)
	}
	if len(failedPre) > 0 {
		sort.Strings(failedPre)
		// 不提交任何计划、不分配版本号、不写对象/动作历史。
		return &Outcome{Class: OutcomePreRejected, ActionType: actionType, CallID: callID,
			FailedPre: failedPre, Accepted: false}
	}

	// 4. 纯函数计划生成；planner 只能读取执行前快照。
	plan, err := a.Plan(input, base)
	if err != nil {
		return &Outcome{Class: OutcomePreRejected, ActionType: actionType, CallID: callID,
			Detail: "plan rejected: " + err.Error()}
	}

	// 5. 全部写入计划生成完毕后，投影出唯一的最终状态快照。
	//    所有后置条件都在这同一个快照上求值（无早/晚快照之分）。
	projected := e.store.projectLocked(base, plan)
	postClauses, err := a.Post(input)
	if err != nil {
		return &Outcome{Class: OutcomePostRejected, ActionType: actionType, CallID: callID,
			Detail: "postcondition construction failed: " + err.Error()}
	}
	for _, clause := range postClauses {
		pass, ev := evaluateClause(clause, projected, env)
		e.store.appendAuditLocked(AuditEntry{CallID: callID, ActionType: actionType,
			Phase: "post", Condition: clause.Name, Input: string(inputJSON),
			Basis: evidenceBasis(ev), Result: passResult(pass)})
		if !pass {
			// 决定性条件：记录后立即放弃，不穷举其余后置条件。
			// 计划整体丢弃：不提交、不分配版本号、不写对象历史。
			// 仅在独立失败轨迹留痕，该轨迹不参与任何对象状态。
			e.store.appendFailureLocked(AuditEntry{CallID: callID, ActionType: actionType,
				Phase: "post", Condition: clause.Name, Input: string(inputJSON),
				Basis: evidenceBasis(ev), Result: "fail"})
			return &Outcome{Class: OutcomePostRejected, ActionType: actionType, CallID: callID,
				FailedPost: clause.Name, Detail: evidenceBasis(ev), Accepted: false}
		}
	}

	// 6. 全部后置条件通过：此刻才提交最终计划并分配版本号。
	e.store.commitLocked(callID, actionType, plan)
	for _, clause := range postClauses {
		e.store.appendAuditLocked(AuditEntry{CallID: callID, ActionType: actionType,
			Phase: "post-commit", Condition: clause.Name, Input: string(inputJSON),
			Basis: "postconditions satisfied; committed final plan", Result: "pass"})
	}
	return &Outcome{Class: OutcomeAccepted, ActionType: actionType, CallID: callID, Accepted: true}
}

// buildEnv 把输入与目标对象绑定成字面量实例化环境。
func buildEnv(input map[string]any, targetIDs []string) map[string]string {
	env := map[string]string{}
	for k, v := range input {
		env[k] = fmt.Sprintf("%v", v)
	}
	if len(targetIDs) > 0 {
		env["target"] = targetIDs[0]
	}
	// targetsSorted 按调用方声明的目标顺序绑定（调用方负责传入稳定顺序，
	// 例如先排序再传入）；执行器不擅自重排，避免与声明的集合语义错位。
	env["targetsSorted"] = strings.Join(targetIDs, ",")
	return env
}

func evidenceBasis(ev []Evidence) string {
	parts := make([]string, 0, len(ev))
	for _, e := range ev {
		parts = append(parts, fmt.Sprintf("%s: observed=%t expect=%t", e.GroundedAtom, e.Observed, e.Expect))
	}
	return strings.Join(parts, "; ")
}

func passResult(pass bool) string {
	if pass {
		return "pass"
	}
	return "fail"
}
