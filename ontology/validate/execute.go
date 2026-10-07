package validate

import (
	"context"
	"fmt"
	"strings"
)

// HookStatus 描述单个钩子在某次调用中的可观察状态。
type HookStatus string

const (
	// StatusRunApproved 钩子实际执行并通过。
	StatusRunApproved HookStatus = "run_approved"
	// StatusRunRejected 钩子实际执行并拒绝。
	StatusRunRejected HookStatus = "run_rejected"
	// StatusRunError 钩子实际执行但自身抛出异常（返回 error 或 panic）。
	StatusRunError HookStatus = "run_error"
	// StatusSkippedShortCircuit 因同组允许短路、前序钩子拒绝而未执行。
	StatusSkippedShortCircuit HookStatus = "skipped_short_circuit"
	// StatusSkippedError 因同组前序钩子异常、分组结果不可判定而未执行。
	StatusSkippedError HookStatus = "skipped_error"
	// StatusSkippedGroupBlocked 因更高优先级分组拒绝/异常而整组未执行。
	// 这是低优先级钩子“看起来未曾执行”的唯一审计痕迹：明确标注为未执行。
	StatusSkippedGroupBlocked HookStatus = "skipped_group_blocked"
)

// GroupOutcome 描述分组级别的判定分类，用于把三类结论分别暴露：
// 允许短路且确实短路拒绝、不允许短路汇总拒绝、钩子异常不可判定。
type GroupOutcome string

const (
	// GroupApproved 分组全部钩子执行完毕且均通过。
	GroupApproved GroupOutcome = "approved"
	// GroupRejectedShortCircuit 允许短路的分组内发生短路拒绝。
	GroupRejectedShortCircuit GroupOutcome = "rejected_short_circuit"
	// GroupRejectedAggregated 不允许短路的分组执行完全部钩子后汇总拒绝。
	GroupRejectedAggregated GroupOutcome = "rejected_aggregated"
	// GroupIndeterminateError 钩子异常，分组结果不可判定（单独上抛）。
	GroupIndeterminateError GroupOutcome = "indeterminate_error"
	// GroupSkippedBlocked 因更高优先级分组拒绝/异常而整组未执行。
	GroupSkippedBlocked GroupOutcome = "skipped_blocked"
)

// HookRecord 是单个钩子的审计记录。未执行的钩子也会留下记录，
// 但 Status 明确标注为 skipped_*，绝不伪装成曾经执行。
type HookRecord[T any] struct {
	Group   string
	ID      string
	Status  HookStatus
	Outcome Outcome
	// Err 仅在 Status == StatusRunError 时非 nil。
	Err error
}

// GroupResult 是一个分组在调用中的执行记录，与快照分组一一对应。
type GroupResult[T any] struct {
	Name    string
	Outcome GroupOutcome
	Records []HookRecord[T]
}

// Report 是一次校验调用的完整审计记录与最终判定。
type Report[T any] struct {
	// Snapshot 是本次调用实际使用的快照（独立值拷贝）。
	Snapshot Snapshot[T]
	// FinalDecision 为最终结论。钩子异常时调用方同时收到 error，
	// 此时该字段无意义（置为 Approve 零值），判定以 error 为准。
	FinalDecision Decision
	// Basis 是最终判定依据的可读说明（首个拒绝点/汇总拒绝/异常点）。
	Basis string
	// Groups 覆盖快照中全部分组（含未执行分组），按快照顺序排列。
	Groups []GroupResult[T]

	ran       int
	rejected  int
	skippedSC int
	skippedGB int
	skippedEr int
	errored   int
}

// HookExecutionError 表示钩子在执行期间自身异常（返回 error 或 panic），
// 导致其所在分组结果不可判定。它必须被单独上抛，不得归并为普通拒绝。
type HookExecutionError struct {
	Group  string
	HookID string
	Err    error
	Panic  any
}

func (e *HookExecutionError) Error() string {
	if e.Panic != nil {
		return fmt.Sprintf("validate: hook %q in group %q panicked: %v", e.HookID, e.Group, e.Panic)
	}
	return fmt.Sprintf("validate: hook %q in group %q failed: %v", e.HookID, e.Group, e.Err)
}

func (e *HookExecutionError) Unwrap() error { return e.Err }

// RanHooks 返回实际执行（进入钩子函数）的钩子数。
func (r *Report[T]) RanHooks() int { return r.ran }

// ErroredHooks 返回自身抛出异常的钩子数（非 0 时 Validate 一定同时返回错误）。
func (r *Report[T]) ErroredHooks() int { return r.errored }

// RejectedHooks 返回实际执行且明确拒绝的钩子数。
func (r *Report[T]) RejectedHooks() int { return r.rejected }

// SkippedHooks 返回本次调用被跳过、未实际执行的钩子总数。
// 该数字由本次快照内的记录直接计数：只遍历快照中的分组与钩子，
// 与注册表历史上注册/注销的总次数无关（历史状态在快照中不存在）。
func (r *Report[T]) SkippedHooks() int {
	return r.skippedSC + r.skippedEr + r.skippedGB
}

// Record 按分组名与钩子 ID 查找审计记录，便于事后核对。
func (r *Report[T]) Record(group, id string) (HookRecord[T], bool) {
	for _, g := range r.Groups {
		if g.Name != group {
			continue
		}
		for _, rec := range g.Records {
			if rec.ID == id {
				return rec, true
			}
		}
	}
	return HookRecord[T]{}, false
}

// HookIDs 返回快照中全部钩子 ID，按分组优先级与组内注册顺序排列。
func (s Snapshot[T]) HookIDs() []string {
	n := 0
	for _, g := range s.Groups {
		n += len(g.Hooks)
	}
	ids := make([]string, 0, n)
	for _, g := range s.Groups {
		for _, h := range g.Hooks {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

func executeSnapshot[T any](ctx context.Context, snap Snapshot[T], target T) (*Report[T], error) {
	report := &Report[T]{
		Snapshot:      snap,
		FinalDecision: Approve,
		Groups:        make([]GroupResult[T], 0, len(snap.Groups)),
	}

	// blocked 表示已有更高优先级分组拒绝；errored 表示已有钩子异常。
	// 两者任一成立，更低优先级分组都不得执行。
	blocked := false
	var execErr *HookExecutionError

	for _, sg := range snap.Groups {
		gr := GroupResult[T]{Name: sg.Name, Records: make([]HookRecord[T], 0, len(sg.Hooks))}

		if blocked || execErr != nil {
			// 整组不可观察：钩子函数绝不被调用，只留下明确标注“未执行”的记录。
			for _, h := range sg.Hooks {
				gr.Records = append(gr.Records, HookRecord[T]{
					Group: sg.Name, ID: h.ID, Status: StatusSkippedGroupBlocked,
				})
				report.skippedGB++
			}
			gr.Outcome = GroupSkippedBlocked
			report.Groups = append(report.Groups, gr)
			continue
		}

		var firstReject *HookRecord[T]
		var stopNow bool // 组内因拒绝（短路）或异常而停止

		for _, h := range sg.Hooks {
			if stopNow {
				status := StatusSkippedShortCircuit
				if execErr != nil {
					status = StatusSkippedError
					report.skippedEr++
				} else {
					report.skippedSC++
				}
				gr.Records = append(gr.Records, HookRecord[T]{
					Group: sg.Name, ID: h.ID, Status: status,
				})
				continue
			}

			outcome, err, panicked := runHook(ctx, h, target)
			report.ran++
			if err != nil {
				report.errored++
				hee := &HookExecutionError{Group: sg.Name, HookID: h.ID}
				if panicked {
					hee.Panic = err
				} else {
					hee.Err = err
				}
				execErr = hee
				gr.Records = append(gr.Records, HookRecord[T]{
					Group: sg.Name, ID: h.ID, Status: StatusRunError, Err: hee,
				})
				gr.Outcome = GroupIndeterminateError
				stopNow = true
				continue
			}

			rec := HookRecord[T]{Group: sg.Name, ID: h.ID, Outcome: outcome}
			if outcome.Decision == Reject {
				rec.Status = StatusRunRejected
				report.rejected++
				if firstReject == nil {
					recCopy := rec
					firstReject = &recCopy
				}
				blocked = true // 组间短路：低优先级分组不再执行
				if sg.ShortCircuit {
					stopNow = true
				}
			} else {
				rec.Status = StatusRunApproved
			}
			gr.Records = append(gr.Records, rec)
		}

		if gr.Outcome == "" {
			switch {
			case firstReject != nil && sg.ShortCircuit:
				gr.Outcome = GroupRejectedShortCircuit
			case firstReject != nil:
				gr.Outcome = GroupRejectedAggregated
			default:
				gr.Outcome = GroupApproved
			}
		}
		report.Groups = append(report.Groups, gr)

		// 异常或拒绝后不提前返回：继续遍历剩余分组，仅为其生成
		// skipped_blocked 审计记录（钩子函数绝不执行），保证报告完整。
		if firstReject != nil {
			report.FinalDecision = Reject
			if sg.ShortCircuit {
				report.Basis = fmt.Sprintf(
					"rejected: group %q short-circuited at hook %q (%s); lower-priority groups were not executed",
					sg.Name, firstReject.ID, reasonOrNone(firstReject.Outcome.Reason))
			} else {
				rejecting := make([]string, 0)
				for _, rec := range gr.Records {
					if rec.Status == StatusRunRejected {
						rejecting = append(rejecting, rec.ID)
					}
				}
				report.Basis = fmt.Sprintf(
					"rejected: group %q aggregated rejection after running all hooks; rejecting hooks: %s; lower-priority groups were not executed",
					sg.Name, strings.Join(rejecting, ","))
			}
		}
	}

	if execErr != nil {
		report.FinalDecision = Approve
		report.Basis = fmt.Sprintf(
			"indeterminate: hook %q in group %q raised an error; group result undecidable and lower-priority groups were not executed",
			execErr.HookID, execErr.Group)
		return report, execErr
	}
	if report.FinalDecision == Approve && report.Basis == "" {
		report.Basis = fmt.Sprintf(
			"approved: %d hook(s) ran across %d group(s) in snapshot version %d",
			report.ran, len(report.Groups), snap.Version)
	}
	return report, nil
}

// runHook 执行单个钩子并把 panic 也收敛为 error，保证钩子异常绝不逃逸为
// 进程级崩溃，而是统一进入“不可判定”通道单独上抛。
func runHook[T any](ctx context.Context, h Hook[T], target T) (out Outcome, err error, panicked bool) {
	defer func() {
		if p := recover(); p != nil {
			out = Outcome{}
			err = fmt.Errorf("%v", p)
			panicked = true
		}
	}()
	out, err = h.Fn(ctx, target)
	return out, err, false
}

func reasonOrNone(reason string) string {
	if reason == "" {
		return "no reason given"
	}
	return reason
}
