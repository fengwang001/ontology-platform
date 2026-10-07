package ontology

import (
	"context"
	"fmt"
)

// Snapshot 是某次校验调用开始时刻的钩子集合快照，提取后不可变。
type Snapshot struct {
	// Version 是快照对应的注册表版本号，随每次注册/注销单调递增。
	Version uint64
	// Groups 按组间优先级升序排列，仅包含有钩子的分组。
	Groups []GroupSnapshot
}

// GroupSnapshot 是快照中一个分组的有序钩子列表。
type GroupSnapshot struct {
	Spec GroupSpec
	// Hooks 按注册顺序（seq 升序）排列。
	Hooks []HookSnapshot
}

// HookSnapshot 是快照中的单个钩子。
type HookSnapshot struct {
	ID   string
	Seq  uint64
	Hook Hook
}

// HookCount 返回快照中的钩子总数。
func (s Snapshot) HookCount() int {
	n := 0
	for _, g := range s.Groups {
		n += len(g.Hooks)
	}
	return n
}

// Outcome 是分组在一次校验中的结果类别。
type Outcome string

const (
	// OutcomePassed 表示组内全部已执行钩子均放行。
	OutcomePassed Outcome = "passed"
	// OutcomeRejectedShortCircuit 表示允许短路的分组内发生拒绝并短路。
	OutcomeRejectedShortCircuit Outcome = "rejected_short_circuit"
	// OutcomeRejectedAggregate 表示不允许短路的分组全部执行完毕后汇总为拒绝。
	OutcomeRejectedAggregate Outcome = "rejected_aggregate"
	// OutcomeIndeterminate 表示钩子异常导致该组结果不可判定。
	OutcomeIndeterminate Outcome = "indeterminate"
	// OutcomeNotExecuted 表示该组因更高优先级分组拒绝或异常而未被执行。
	OutcomeNotExecuted Outcome = "not_executed"
)

// HookStatus 是单个钩子在一次校验中的执行状态。
type HookStatus string

const (
	// HookPassed 表示钩子执行并放行。
	HookPassed HookStatus = "passed"
	// HookRejected 表示钩子执行并拒绝。
	HookRejected HookStatus = "rejected"
	// HookErrored 表示钩子执行期间抛出异常。
	HookErrored HookStatus = "error"
	// HookSkipped 表示所在分组已开始执行，但该钩子因组内短路或异常而未运行。
	HookSkipped HookStatus = "skipped"
	// HookNotExecuted 表示所在分组整体未被执行（审计用，明确标注为未执行）。
	HookNotExecuted HookStatus = "not_executed"
)

// HookRecord 记录单个钩子在一次校验中的结论。
type HookRecord struct {
	ID     string
	Seq    uint64
	Status HookStatus
	// Err 仅当 Status 为 HookErrored 时非空。
	Err error
}

// GroupRecord 记录一个分组在一次校验中的过程与结果。
type GroupRecord struct {
	Group   GroupSpec
	Outcome Outcome
	Hooks   []HookRecord
}

// Report 是一次校验调用的完整审计记录。
type Report struct {
	// SnapshotVersion 是本次调用使用的快照版本。
	SnapshotVersion uint64
	// Decision 是最终判定；仅当 Validate 返回的 error 为空时有意义。
	Decision Decision
	// Groups 按执行顺序记录每个分组；未执行的分组以 OutcomeNotExecuted 标注。
	Groups []GroupRecord
}

// SkippedCount 返回本次调用中未执行的钩子总数（组内跳过 + 整组未执行）。
// 开销只与本次快照中的钩子数量相关，与历史注册/注销总次数无关。
func (r Report) SkippedCount() int {
	n := 0
	for _, g := range r.Groups {
		for _, h := range g.Hooks {
			if h.Status == HookSkipped || h.Status == HookNotExecuted {
				n++
			}
		}
	}
	return n
}

// HookError 表示钩子执行期间抛出异常，导致所在分组结果不可判定。
// 该错误单独上抛，不得归并为普通拒绝。
type HookError struct {
	Group  string
	HookID string
	Err    error
}

func (e *HookError) Error() string {
	return fmt.Sprintf("ontology: hook %q in group %q failed: %v", e.HookID, e.Group, e.Err)
}

func (e *HookError) Unwrap() error {
	return e.Err
}

// Validate 对目标执行一次完整校验。
//
// 调用开始时提取一次快照，本次调用全程只使用该快照；
// 调用过程中的注册/注销不影响本次调用。
// 返回的 Report 记录快照内容与各钩子结论；
// 若某个钩子抛出异常，返回的 error 为 *HookError，且更低优先级分组一律未执行。
func (r *Registry) Validate(ctx context.Context, target Target) (Report, error) {
	snap := r.Snapshot()
	rep := Report{SnapshotVersion: snap.Version, Decision: Allow}

	halted := false
	for _, gs := range snap.Groups {
		gr := GroupRecord{Group: gs.Spec, Outcome: OutcomePassed}
		gr.Hooks = make([]HookRecord, 0, len(gs.Hooks))
		if halted {
			// 更高优先级分组已拒绝或异常：本组不得执行，
			// 仅保留明确标注为未执行的审计记录。
			gr.Outcome = OutcomeNotExecuted
			for _, h := range gs.Hooks {
				gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookNotExecuted})
			}
			rep.Groups = append(rep.Groups, gr)
			continue
		}

		groupRejected := false
		for i, h := range gs.Hooks {
			if groupRejected && gs.Spec.ShortCircuit {
				// 组内短路：后续钩子不执行。
				gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookSkipped})
				continue
			}
			if err := ctx.Err(); err != nil {
				// 调用方取消：按不可判定处理，剩余钩子标注为跳过。
				gr.Outcome = OutcomeIndeterminate
				rep.Groups = append(rep.Groups, gr)
				markNotExecuted(&rep, snap.Groups[len(rep.Groups):])
				return rep, &HookError{Group: gs.Spec.Name, HookID: h.ID, Err: err}
			}
			decision, err := h.Hook.Validate(ctx, target)
			switch {
			case err != nil:
				// 钩子异常：该组结果不可判定，单独上抛；
				// 组内剩余钩子跳过，更低优先级分组一律不执行。
				gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookErrored, Err: err})
				for _, rest := range gs.Hooks[i+1:] {
					gr.Hooks = append(gr.Hooks, HookRecord{ID: rest.ID, Seq: rest.Seq, Status: HookSkipped})
				}
				gr.Outcome = OutcomeIndeterminate
				rep.Groups = append(rep.Groups, gr)
				markNotExecuted(&rep, snap.Groups[len(rep.Groups):])
				return rep, &HookError{Group: gs.Spec.Name, HookID: h.ID, Err: err}
			case decision == Reject:
				gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookRejected})
				groupRejected = true
			default:
				gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookPassed})
			}
		}

		if groupRejected {
			if gs.Spec.ShortCircuit {
				gr.Outcome = OutcomeRejectedShortCircuit
			} else {
				gr.Outcome = OutcomeRejectedAggregate
			}
			rep.Decision = Reject
			halted = true
		}
		rep.Groups = append(rep.Groups, gr)
	}
	return rep, nil
}

// markNotExecuted 把剩余分组整体标注为未执行。
func markNotExecuted(rep *Report, rest []GroupSnapshot) {
	for _, gs := range rest {
		gr := GroupRecord{Group: gs.Spec, Outcome: OutcomeNotExecuted}
		gr.Hooks = make([]HookRecord, 0, len(gs.Hooks))
		for _, h := range gs.Hooks {
			gr.Hooks = append(gr.Hooks, HookRecord{ID: h.ID, Seq: h.Seq, Status: HookNotExecuted})
		}
		rep.Groups = append(rep.Groups, gr)
	}
}
