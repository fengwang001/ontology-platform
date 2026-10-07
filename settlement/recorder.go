package settlement

import (
	"fmt"
	"reflect"
	"sync"
)

// OpKind 操作类型。
type OpKind int

const (
	OpCreateContract OpKind = iota + 1
	OpAccept
	OpSettle
	OpRegisterDefect
	OpCloseDefect
	OpRelease
	OpAmend
	OpTerminate
)

func (k OpKind) String() string {
	switch k {
	case OpCreateContract:
		return "CreateContract"
	case OpAccept:
		return "Accept"
	case OpSettle:
		return "Settle"
	case OpRegisterDefect:
		return "RegisterDefect"
	case OpCloseDefect:
		return "CloseDefect"
	case OpRelease:
		return "Release"
	case OpAmend:
		return "Amend"
	case OpTerminate:
		return "Terminate"
	default:
		return fmt.Sprintf("OpKind(%d)", int(k))
	}
}

// Op 一次操作的完整输入，可被重放。
type Op struct {
	Kind         OpKind
	ContractID   string
	Spec         ContractSpec // OpCreateContract
	MilestoneID  string
	DefectID     string
	Passed       bool     // OpAccept
	Forfeit      int64    // OpRegisterDefect
	EffectiveDay int64    // OpAmend
	Changes      []Change // OpAmend
	Now          int64
}

func (o Op) String() string {
	switch o.Kind {
	case OpCreateContract:
		return fmt.Sprintf("CreateContract(id=%q total=%d advance=%d/%d retention=%d warranty=%dd penalty=%d/%d milestones=%v now=%d)",
			o.ContractID, o.Spec.TotalAmount, o.Spec.AdvanceTotal, o.Spec.AdvanceRatio,
			o.Spec.RetentionRatio, o.Spec.WarrantyDays, o.Spec.PenaltyDailyRate,
			o.Spec.PenaltyCapRatio, o.Spec.Milestones, o.Now)
	case OpAccept:
		return fmt.Sprintf("Accept(c=%q m=%q passed=%v now=%d)", o.ContractID, o.MilestoneID, o.Passed, o.Now)
	case OpSettle:
		return fmt.Sprintf("Settle(c=%q m=%q now=%d)", o.ContractID, o.MilestoneID, o.Now)
	case OpRegisterDefect:
		return fmt.Sprintf("RegisterDefect(c=%q m=%q d=%q forfeit=%d now=%d)",
			o.ContractID, o.MilestoneID, o.DefectID, o.Forfeit, o.Now)
	case OpCloseDefect:
		return fmt.Sprintf("CloseDefect(c=%q m=%q d=%q now=%d)", o.ContractID, o.MilestoneID, o.DefectID, o.Now)
	case OpRelease:
		return fmt.Sprintf("Release(c=%q m=%q now=%d)", o.ContractID, o.MilestoneID, o.Now)
	case OpAmend:
		return fmt.Sprintf("Amend(c=%q eff=%d changes=%v now=%d)", o.ContractID, o.EffectiveDay, o.Changes, o.Now)
	case OpTerminate:
		return fmt.Sprintf("Terminate(c=%q now=%d)", o.ContractID, o.Now)
	default:
		return fmt.Sprintf("Op(%d)", int(o.Kind))
	}
}

// Outcome 一次操作的结果：错误码与各类操作返回值。
type Outcome struct {
	ErrCode ErrorCode
	Err     *Error
	Accept  *AcceptResult
	Settle  *SettleResult
	Release *ReleaseResult
}

func ok() Outcome { return Outcome{} }

func fail(e *Error) Outcome {
	return Outcome{ErrCode: e.Code, Err: e}
}

func (o Outcome) err() error {
	if o.Err == nil {
		return nil
	}
	return o.Err
}

// OpEvent 一条已执行操作及其结果，构成可重放日志。
type OpEvent struct {
	Op      Op
	Outcome Outcome
}

// Recorder 操作记录器。append 为 O(1) 均摊，不影响操作与查询的渐近复杂度；
// 不挂载记录器时服务无任何随历史增长的开销。
type Recorder struct {
	mu     sync.Mutex
	Events []OpEvent
}

func (r *Recorder) append(ev OpEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, ev)
}

// Snapshot 返回当前记录的事件副本。
func (r *Recorder) Snapshot() []OpEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpEvent, len(r.Events))
	copy(out, r.Events)
	return out
}

// Replay 在全新服务上按序重放事件，逐步校验错误码与结果完全一致，
// 以此证明“相同操作序列重放得到完全相同的结果”。返回重放后的服务。
func Replay(events []OpEvent) (*Service, error) {
	s := NewService()
	for i, ev := range events {
		got := s.Execute(ev.Op)
		if got.ErrCode != ev.Outcome.ErrCode {
			return nil, fmt.Errorf("第 %d 步 %s: 错误码不一致: 记录=%s 重放=%s",
				i, ev.Op, ev.Outcome.ErrCode, got.ErrCode)
		}
		if !reflect.DeepEqual(got.Accept, ev.Outcome.Accept) ||
			!reflect.DeepEqual(got.Settle, ev.Outcome.Settle) ||
			!reflect.DeepEqual(got.Release, ev.Outcome.Release) {
			return nil, fmt.Errorf("第 %d 步 %s: 结果不一致: 记录=%+v/%+v/%+v 重放=%+v/%+v/%+v",
				i, ev.Op, ev.Outcome.Accept, ev.Outcome.Settle, ev.Outcome.Release,
				got.Accept, got.Settle, got.Release)
		}
	}
	return s, nil
}
