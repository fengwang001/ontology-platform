package coord

import (
	"errors"

	"ontology/participant"
	"ontology/vote"
)

var (
	// ErrDuplicateParticipant: the same participant ID registered twice.
	ErrDuplicateParticipant = errors.New("coord: duplicate participant")
	// ErrUnknownParticipant: instruction sent to an unregistered ID.
	ErrUnknownParticipant = errors.New("coord: unknown participant")
	// ErrNotDecided: replay requested before any decision was recorded.
	ErrNotDecided = errors.New("coord: no decision recorded yet")
)

// Phase is the transaction's current phase.
type Phase int

const (
	PhaseVoting Phase = iota
	PhaseCommit
	PhaseAbort
)

func (p Phase) String() string {
	switch p {
	case PhaseCommit:
		return "commit"
	case PhaseAbort:
		return "abort"
	default:
		return "voting"
	}
}

// Instruction is a second-phase directive to a participant.
type Instruction int

const (
	InstrCommit Instruction = iota
	InstrAbort
)

// ParticipantStatus is a snapshot of one participant.
type ParticipantStatus struct {
	ID    string
	State participant.State
}

// Snapshot is a consistent read-only view of the transaction. Decision
// is the zero value while the transaction is undecided.
type Snapshot struct {
	Phase    Phase
	Decision vote.Decision
	Members  []ParticipantStatus
}
