package journal

// Phase is the lifecycle stage recorded for one step.
type Phase uint8

const (
	PhaseStart      Phase = 1 // execution began (attempt number implicit via count)
	PhaseDone       Phase = 2 // execution succeeded exactly once
	PhaseFailed     Phase = 3 // final failure after retries exhausted
	PhaseCompStart  Phase = 4 // compensation began
	PhaseCompDone   Phase = 5 // compensation succeeded exactly once
	PhaseCompFailed Phase = 6 // compensation failed (terminal)
)

func (p Phase) String() string {
	switch p {
	case PhaseStart:
		return "start"
	case PhaseDone:
		return "done"
	case PhaseFailed:
		return "failed"
	case PhaseCompStart:
		return "comp-start"
	case PhaseCompDone:
		return "comp-done"
	case PhaseCompFailed:
		return "comp-failed"
	default:
		return "unknown"
	}
}

// Record is one immutable entry in the execution trail.
type Record struct {
	Seq    int    // 1-based append order, assigned by the journal
	StepID string // step the entry belongs to
	Phase  Phase  // lifecycle phase
	Detail string // optional error message for failure phases
}
