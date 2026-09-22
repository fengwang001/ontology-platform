package saga

import (
	"fmt"

	"ontology/journal"
)

// Status 是实例的生命周期状态。
type Status int

const (
	// Running：正向尚未全部成功。
	Running Status = iota
	// Succeeded：所有步骤正向成功，无补偿。
	Succeeded
	// Compensated：补偿阶段所有已成功步骤都补偿成功。
	Compensated
	// CompensateFailed：补偿阶段有补偿失败，但其余补偿已继续执行。
	CompensateFailed
)

func (s Status) String() string {
	switch s {
	case Succeeded:
		return "succeeded"
	case Compensated:
		return "compensated"
	case CompensateFailed:
		return "compensate-failed"
	default:
		return "running"
	}
}

// State 是一个实例可被查询的全部状态。它只能由日志归约得到。
type State struct {
	InstanceID string
	Status     Status
	// Succeeded：正向成功（含推定成功）的步骤下标，升序。
	Succeeded []int
	// Compensated：补偿成功的步骤下标，升序。
	Compensated []int
	// CompensateFailures：补偿失败的步骤下标，升序；非空时 Status=CompensateFailed。
	CompensateFailures []int
	// ForwardFailed：最终正向失败的步骤下标（-1 表示无）。
	ForwardFailed int
	// UnknownSucceeded：以「未知结果」按推定成功记录的步骤下标。
	UnknownSucceeded []int
	Records          int
}

// reconstruct 是状态的唯一归约函数：在线状态与离线重建都调用它。
// n 为该实例的步骤总数。
func reconstruct(id string, recs []journal.Record, n int) State {
	st := State{InstanceID: id, ForwardFailed: -1, Records: len(recs)}
	compFailed := map[int]bool{}
	for _, r := range recs {
		switch {
		case r.Direction == journal.Forward && r.Result == journal.Success:
			st.Succeeded = insertInt(st.Succeeded, r.StepIndex)
			if r.Unknown {
				st.UnknownSucceeded = insertInt(st.UnknownSucceeded, r.StepIndex)
			}
		case r.Direction == journal.Forward && r.Result == journal.Failure:
			st.ForwardFailed = r.StepIndex
		case r.Direction == journal.Compensate && r.Result == journal.Success:
			compFailed[r.StepIndex] = false
			st.Compensated = insertInt(st.Compensated, r.StepIndex)
		case r.Direction == journal.Compensate && r.Result == journal.Failure:
			compFailed[r.StepIndex] = true
		}
	}
	for i, fail := range compFailed {
		if fail {
			st.CompensateFailures = insertInt(st.CompensateFailures, i)
		}
	}
	switch {
	case len(st.Compensated) > 0 || len(st.CompensateFailures) > 0:
		if len(st.CompensateFailures) > 0 {
			st.Status = CompensateFailed
		} else {
			st.Status = Compensated
		}
	case st.ForwardFailed >= 0:
		// 正向已终结失败且补偿阶段走完（可能为空补偿，例如第一步就失败）。
		st.Status = Compensated
	case len(st.Succeeded) == n:
		st.Status = Succeeded
	default:
		st.Status = Running
	}
	return st
}

func insertInt(xs []int, v int) []int {
	for i, x := range xs {
		if x == v {
			return xs
		}
		if x > v {
			xs = append(xs, 0)
			copy(xs[i+1:], xs[i:])
			xs[i] = v
			return xs
		}
	}
	return append(xs, v)
}

// ValidateRecordSequence 校验单个实例的记录序列是否合法（只依赖日志）。
func ValidateRecordSequence(recs []journal.Record, n int) error {
	if n <= 0 {
		return fmt.Errorf("sequence: invalid step count %d", n)
	}
	fwdSuccess := map[int]bool{}
	compSuccess := map[int]bool{}
	compensating := false
	for _, r := range recs {
		if r.StepIndex < 0 || r.StepIndex >= n {
			return fmt.Errorf("sequence: step index %d out of range", r.StepIndex)
		}
		if r.Direction == journal.Compensate {
			compensating = true
			if !fwdSuccess[r.StepIndex] {
				return fmt.Errorf("sequence: compensate at step %d before forward success", r.StepIndex)
			}
			if r.Result == journal.Success {
				if compSuccess[r.StepIndex] {
					return fmt.Errorf("sequence: duplicate compensate success at step %d", r.StepIndex)
				}
				compSuccess[r.StepIndex] = true
			}
			continue
		}
		if compensating {
			return fmt.Errorf("sequence: forward record at step %d after compensation began", r.StepIndex)
		}
		if r.Result == journal.Success {
			if fwdSuccess[r.StepIndex] {
				return fmt.Errorf("sequence: duplicate forward success at step %d", r.StepIndex)
			}
			fwdSuccess[r.StepIndex] = true
		}
	}
	return nil
}
