package ontology

// Phase 标记事件发生在调用处理的哪个阶段。
type Phase string

const (
	PhaseGuard Phase = "self-trigger-guard"
	PhasePre   Phase = "precondition"
	PhaseRun   Phase = "run"
	PhasePost  Phase = "postcondition"
)

// Outcome 是一次调用的结论类别。
type Outcome int

const (
	OutcomeOK Outcome = iota
	// OutcomePreconditionFailed 内层前置条件未通过。
	OutcomePreconditionFailed
	// OutcomePostconditionFailedCritical 内层后置条件未通过且为关键调用，整体放弃。
	OutcomePostconditionFailedCritical
	// OutcomeRunFailedCritical 内层执行出错且为关键调用，整体放弃。
	OutcomeRunFailedCritical
	// OutcomeNonCriticalFailed 非关键调用失败，外层继续。
	OutcomeNonCriticalFailed
	// OutcomeSelfTriggerRejected 检测到自我触发，触发前拒绝。
	OutcomeSelfTriggerRejected
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomePreconditionFailed:
		return "precondition-failed"
	case OutcomePostconditionFailedCritical:
		return "postcondition-failed-critical"
	case OutcomeRunFailedCritical:
		return "run-failed-critical"
	case OutcomeNonCriticalFailed:
		return "non-critical-failed"
	case OutcomeSelfTriggerRejected:
		return "self-trigger-rejected"
	default:
		return "unknown"
	}
}

// Basis 记录一次条件求值所依据的状态：快照版本 + 当时可见的未提交写入。
type Basis struct {
	BaseVersion   uint64
	PendingWrites []string
}

// Event 是调用轨迹中的一条记录。
type Event struct {
	Path      string
	Depth     int
	Action    string
	ArgsKey   string
	Critical  bool
	Phase     Phase
	PreBasis  Basis
	PostBasis Basis
	Outcome   Outcome
	Detail    string
}

// Trace 是一条调用链条的完整审计轨迹。
type Trace struct {
	events      []Event
	committed   bool
	conclusion  string
	guardChecks int
}

func (t *Trace) add(e Event) { t.events = append(t.events, e) }

func (t *Trace) Events() []Event {
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

func (t *Trace) Committed() bool { return t.committed }

func (t *Trace) Conclusion() string { return t.conclusion }

// GuardChecks 返回本链条中自我触发守卫的查表次数，用于验证开销上界。
func (t *Trace) GuardChecks() int { return t.guardChecks }
