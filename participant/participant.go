// Package participant implements the local state machine of a two-phase
// commit participant: pending -> prepared -> committed/aborted, with
// idempotent commit/abort instructions.
package participant

import (
	"errors"
	"sync"

	"ontology/vote"
)

// State is the local state of a participant.
type State int

const (
	StatePending State = iota
	StatePrepared
	StateCommitted
	StateAborted
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StatePrepared:
		return "prepared"
	case StateCommitted:
		return "committed"
	case StateAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// Illegal-transition errors. They are distinct sentinels so callers can
// tell them apart with errors.Is.
var (
	// ErrNotPrepared: commit arrived before prepare.
	ErrNotPrepared = errors.New("participant: commit before prepare")
	// ErrCommitAfterAbort: commit arrived after the participant aborted.
	ErrCommitAfterAbort = errors.New("participant: commit after abort")
	// ErrAbortAfterCommit: abort arrived after the participant committed.
	ErrAbortAfterCommit = errors.New("participant: abort after commit")
)

// Participant is a single 2PC participant. It is safe for concurrent use.
type Participant struct {
	mu          sync.Mutex
	id          string
	ballot      vote.Ballot
	state       State
	commits     int
	aborts      int
	prepareHook func()
}

// New creates a participant that will answer prepare with the given ballot.
func New(id string, ballot vote.Ballot) *Participant {
	return &Participant{id: id, ballot: ballot, state: StatePending}
}

// SetPrepareHook installs a hook run inside Prepare before the vote is
// answered. It is meant for tests and demos (e.g. advancing a fake clock
// to simulate a slow participant). The hook must not call back into the
// participant.
func (p *Participant) SetPrepareHook(hook func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prepareHook = hook
}

// ID returns the participant's ID.
func (p *Participant) ID() string { return p.id }

// Prepare answers the first-phase vote. A pending participant transitions
// to prepared (agree) or aborted (reject). Re-asking a decided
// participant returns the vote implied by its current state.
func (p *Participant) Prepare() vote.Ballot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prepareHook != nil {
		p.prepareHook()
	}
	switch p.state {
	case StatePending:
		if p.ballot == vote.BallotAgree {
			p.state = StatePrepared
		} else {
			p.state = StateAborted
		}
		return p.ballot
	case StatePrepared, StateCommitted:
		return vote.BallotAgree
	default:
		return vote.BallotReject
	}
}

// Commit applies the commit instruction. It is idempotent: a repeated
// commit on an already committed participant is a no-op and does not
// increment CommitCount.
func (p *Participant) Commit() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case StatePrepared:
		p.state = StateCommitted
		p.commits++
		return nil
	case StateCommitted:
		return nil
	case StatePending:
		return ErrNotPrepared
	default:
		return ErrCommitAfterAbort
	}
}

// Abort applies the abort instruction. It is idempotent: a repeated
// abort on an already aborted participant is a no-op.
func (p *Participant) Abort() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case StatePending, StatePrepared:
		p.state = StateAborted
		p.aborts++
		return nil
	case StateAborted:
		return nil
	default:
		return ErrAbortAfterCommit
	}
}

// State returns the current local state.
func (p *Participant) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// CommitCount reports how many times the local commit action actually
// executed. Idempotent repeats do not increase it.
func (p *Participant) CommitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commits
}

// AbortCount reports how many times the local abort action actually
// executed. Idempotent repeats do not increase it.
func (p *Participant) AbortCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aborts
}
