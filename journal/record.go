package journal

// Phase of a step recorded in the journal.
type Phase string

const (
	// PhaseExecuting marks the start of one execution attempt.
	PhaseExecuting Phase = "executing"
	// PhaseCompleted marks a successful execution.
	PhaseCompleted Phase = "completed"
	// PhaseFailed marks a permanently failed execution (retries exhausted).
	PhaseFailed Phase = "failed"
	// PhaseCompensating marks the start of one compensation attempt.
	PhaseCompensating Phase = "compensating"
	// PhaseCompensated marks a successful compensation.
	PhaseCompensated Phase = "compensated"
	// PhaseCompFailed marks a permanently failed compensation.
	PhaseCompFailed Phase = "compfailed"
)

// Record is one immutable entry in the execution trail. Seq is the
// zero-based ordinal assigned at append time. Attempt is the zero-based
// attempt number (both for execution and compensation retries).
type Record struct {
	Seq       int    `json:"seq"`
	StepID    string `json:"step"`
	Phase     Phase  `json:"phase"`
	Attempt   int    `json:"attempt"`
	Exhausted bool   `json:"exhausted,omitempty"`
	Err       string `json:"err,omitempty"`
}

// AttemptEvents groups the attempt counter implied by a record. Replay is the
// only source of truth: each record has one well-defined counter effect, so
// the same journal replayed any number of times yields identical state.
func (r Record) AttemptEvents() (executes, compensates int) {
	switch r.Phase {
	case PhaseExecuting:
		return 1, 0
	case PhaseCompensating:
		return 0, 1
	default:
		return 0, 0
	}
}
