package compensation

import (
	"context"
	"fmt"
)

// Executor 执行带补偿回滚保证的动作。
type Executor struct {
	graph  *Graph
	tracer Tracer
}

// New 创建执行器。
func New(graph *Graph, tracer Tracer) *Executor {
	return &Executor{graph: graph, tracer: tracer}
}

// Execute 按声明顺序执行动作的全部子操作；任一子操作失败时
// 按生效顺序的逆序补偿此前已生效的子操作。
//
// 判定优先级（与是否发生补偿、补偿是否成功无关）：
//
//  1. CategoryContaminated      预检发现污染实例，发生在任何子操作之前
//  2. CategoryBusinessRejected  某子操作业务校验/结构错误，拒绝不产生副作用
//  3. CategoryContention        守卫争用，拒绝发生在任何可观察改动之前
//  4. CategoryCompensationFailed 子操作失败触发补偿，且至少一个逆操作失败/异常
//
// 子操作全部成功 -> CategoryOK。
func (e *Executor) Execute(_ context.Context, action *Action) *Outcome {
	out := &Outcome{ActionID: action.ID, FailedStep: -1}
	if e.tracer != nil {
		e.tracer.ActionStart(action.ID, len(action.Ops))
	}

	// 阶段 0：污染预检——必须在新动作执行任何子操作之前完成。
	var involved []string
	for _, op := range action.Ops {
		involved = append(involved, opObjectIDs(op)...)
	}
	if info, tainted := e.graph.firstTainted(involved); tainted {
		out.Category = CategoryContaminated
		out.Reason = fmt.Sprintf("object %s is contaminated (earliest failed step %d, entry #%d)",
			info.ObjectID, info.EarliestStep, info.Entry)
		out.RejectedBeforeEffect = true
		e.traceReject(action.ID, -1, CategoryContaminated, out.Reason)
		e.traceDone(action.ID, out.Category, out.Reason)
		return out
	}

	// 阶段 1：在任何生效之前一次性获取全部守卫；争用立即拒绝，
	// 不触碰任何属性、链接、时钟戳或版本号。
	held, acquired := e.graph.acquireGuards(action.Ops)
	if !acquired {
		out.Category = CategoryContention
		out.Reason = "guard contention on overlapping property/link before any effect"
		out.RejectedBeforeEffect = true
		e.traceReject(action.ID, -1, CategoryContention, out.Reason)
		e.traceDone(action.ID, out.Category, out.Reason)
		return out
	}
	defer e.graph.releaseGuards(held)

	// 阶段 2：持锁后再查一次污染，封住「预检后、获锁前」被并发补偿污染的窗口。
	if info, tainted := e.graph.firstTainted(involved); tainted {
		out.Category = CategoryContaminated
		out.Reason = fmt.Sprintf("object %s became contaminated before first effect (step %d, entry #%d)",
			info.ObjectID, info.EarliestStep, info.Entry)
		out.RejectedBeforeEffect = true
		e.traceReject(action.ID, -1, CategoryContaminated, out.Reason)
		e.traceDone(action.ID, out.Category, out.Reason)
		return out
	}

	// 阶段 3：按声明顺序依次生效；每个子操作在同一临界区内完成生效+逆操作登记。
	stack := make([]*inverse, 0, len(action.Ops))
	failedAt := -1
	for i, op := range action.Ops {
		if op.Check != nil && !op.Check() {
			failedAt = i
			out.Reason = fmt.Sprintf("business check rejected at step %d", i)
			e.traceReject(action.ID, i, CategoryBusinessRejected, out.Reason)
			break
		}
		detail, err := e.effectAndRegister(op, i, &stack)
		if err != nil {
			failedAt = i
			out.Reason = fmt.Sprintf("business/structural rejection at step %d: %v", i, err)
			e.traceReject(action.ID, i, CategoryBusinessRejected, err.Error())
			break
		}
		last := stack[len(stack)-1]
		out.Applied = append(out.Applied, StepEffect{Index: i, Entry: last.entry, Detail: detail})
		if e.tracer != nil {
			e.tracer.StepApplied(action.ID, i, last.entry, detail)
		}
	}

	if failedAt == -1 {
		// 所有子操作生效，无补偿；逆操作登记随栈丢弃即可（未被需要）。
		out.Category = CategoryOK
		out.Reason = "all steps applied"
		e.traceDone(action.ID, out.Category, out.Reason)
		return out
	}
	out.FailedStep = failedAt

	// 阶段 4：仅补偿失败点之前已生效的子操作，严格逆序。
	// 失败的子操作本身与其后子操作都不在栈中，天然不会被补偿。
	out.CompFailures = e.compensate(action.ID, stack)
	bad := map[int]struct{}{}
	for _, rec := range out.CompFailures {
		bad[rec.Index] = struct{}{}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		inv := stack[i]
		if _, isBad := bad[inv.step]; isBad {
			continue
		}
		out.Compensated = append(out.Compensated, inv.step)
	}

	if len(out.CompFailures) == 0 {
		out.Category = CategoryBusinessRejected
		e.traceDone(action.ID, out.Category, out.Reason)
		return out
	}

	// 阶段 5：把所有补偿失败环节涉及的实例标记污染（最早编号永不被覆盖）。
	out.Tainted = e.graph.commitTaints(out.CompFailures)
	out.Category = CategoryCompensationFailed
	out.Reason = fmt.Sprintf("%s; %d inverse op(s) failed during compensation",
		out.Reason, len(out.CompFailures))
	e.traceDone(action.ID, out.Category, out.Reason)
	return out
}

// compensate 按登记顺序的逆序逐一执行逆操作。
// 逆操作返回失败或抛出异常都只记录、不中止；异常被转换为补偿失败记录。
// 一旦某实例已出现在失败记录中，后续涉及该实例的逆操作一律跳过，
// 以保证失败实例的对象图状态停留在补偿失败前的样子，不被进一步改动。
func (e *Executor) compensate(actionID string, stack []*inverse) []CompRecord {
	if e.tracer != nil {
		e.tracer.CompStart(actionID, len(stack))
	}

	poisoned := map[string]struct{}{}
	var failures []CompRecord

	for i := len(stack) - 1; i >= 0; i-- {
		inv := stack[i]
		rec := CompRecord{Index: inv.step, Entry: inv.entry, ObjectIDs: inv.objects}

		if touches(poisoned, inv.objects) {
			rec.Skipped = true
			rec.Reason = "skipped: object already poisoned by an earlier compensation failure"
			failures = append(failures, rec)
			if e.tracer != nil {
				e.tracer.CompStep(actionID, rec)
			}
			// 跳过只意味着状态冻结，不是新的污染源；污染只能由硬失败产生。
			continue
		}

		ok, panicked, reason := inv.run()
		rec.OK = ok
		rec.Panicked = panicked
		rec.Reason = reason
		if !ok {
			failures = append(failures, rec)
			for _, id := range inv.objects {
				poisoned[id] = struct{}{}
			}
		}
		if e.tracer != nil {
			e.tracer.CompStep(actionID, rec)
		}
	}

	if e.tracer != nil {
		e.tracer.CompDone(actionID)
	}
	return failures
}

func touches(set map[string]struct{}, ids []string) bool {
	for _, id := range ids {
		if _, ok := set[id]; ok {
			return true
		}
	}
	return false
}

func (e *Executor) traceReject(actionID string, step int, category Category, reason string) {
	if e.tracer != nil {
		e.tracer.StepRejected(actionID, step, category, reason)
	}
}

func (e *Executor) traceDone(actionID string, category Category, reason string) {
	if e.tracer != nil {
		e.tracer.ActionDone(actionID, category, reason)
	}
}
