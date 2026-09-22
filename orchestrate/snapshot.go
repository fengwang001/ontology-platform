package orchestrate

import "ontology/step"

// Phase is the externally visible lifecycle phase of the whole run.
type Phase uint8

const (
	PhaseIdle         Phase = 0 // constructed, never run
	PhaseExecuting    Phase = 1 // forward execution in progress
	PhaseCompensating Phase = 2 // compensation in progress (hidden per rule 8)
	PhaseCompleted    Phase = 3 // every step Done
	PhaseAborted      Phase = 4 // every step reached a post-compensation terminal
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseExecuting:
		return "executing"
	case PhaseCompensating:
		return "compensating"
	case PhaseCompleted:
		return "completed"
	case PhaseAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// Terminal reports one of the only two externally visible end states.
func (p Phase) Terminal() bool { return p == PhaseCompleted || p == PhaseAborted }

// StepView is the immutable per-step view returned by queries.
type StepView struct {
	ID            string
	State         step.State
	ExecCount     int
	CompCount     int
	FailureDetail string
	CompFailure   string
}

// Snapshot is a consistent read-only answer to a query. Taking two snapshots
// without mutating calls in between yields byte-identical values.
type Snapshot struct {
	Phase        Phase
	Outcome      Phase // alias of Phase for terminal call sites
	Steps        []StepView
	JournalLen   int
	MaxConcSeen  int
	Failure      string        // terminal execution failure, if aborted
	CompFailures []CompFailure // every compensation that failed
}

// CompFailure names one step whose compensation failed and why.
type CompFailure struct {
	StepID string
	Detail string
}
