// Package coord implements a two-phase commit coordinator.
//
// The coordinator asks every registered participant to prepare, collects
// the ballots through a vote.Collector, and commits only on unanimous
// agreement. Any reject or timeout aborts the whole transaction. The
// decision is written once and never changes; commit/abort instructions
// are idempotent on the participants.
//
// Time comes only from the injected clock (now func() time.Time). The
// deadline check is left-closed/right-open: a participant whose answer
// is not in before now == deadline counts as timed out.
//
// Empty participant set: the transaction COMMITS. Unanimous agreement
// over an empty set is vacuously true, and committing is the only
// verdict consistent with "abort only on reject or timeout".
package coord

import (
	"sync"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// Txn is one two-phase commit transaction. It is safe for concurrent use.
type Txn struct {
	mu       sync.Mutex
	now      func() time.Time
	timeout  time.Duration
	order    []string
	members  map[string]*participant.Participant
	decided  bool
	decision vote.Decision
	phase    Phase
}

// NewTxn creates a transaction. now is the only time source; timeout is
// the first-phase deadline budget measured from the start of Drive.
func NewTxn(now func() time.Time, timeout time.Duration) *Txn {
	return &Txn{
		now:     now,
		timeout: timeout,
		members: make(map[string]*participant.Participant),
		phase:   PhaseVoting,
	}
}

// Register adds a participant. Registering the same ID twice fails.
func (t *Txn) Register(p *participant.Participant) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.members[p.ID()]; ok {
		return ErrDuplicateParticipant
	}
	t.members[p.ID()] = p
	t.order = append(t.order, p.ID())
	return nil
}

// Drive runs both phases and returns the recorded decision. It is
// idempotent: once decided, repeated calls return the first decision
// without touching the participants again.
func (t *Txn) Drive() vote.Decision {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.decided {
		return t.decision
	}
	collector, err := vote.NewCollector(t.order)
	if err != nil {
		panic("coord: registration invariant violated: " + err.Error())
	}
	deadline := t.now().Add(t.timeout)
	for _, id := range t.order {
		if t.expired(deadline) {
			_ = collector.Timeout(id)
			continue
		}
		ballot := t.members[id].Prepare()
		if t.expired(deadline) {
			// The answer arrived at or after the deadline: timeout.
			_ = collector.Timeout(id)
			continue
		}
		_ = collector.Cast(id, ballot)
	}
	decision, ok := collector.Decision()
	if !ok {
		panic("coord: collector undecided after full first phase")
	}
	t.decision = decision
	t.decided = true
	t.deliverLocked()
	return t.decision
}

// Send delivers a single instruction to one registered participant.
func (t *Txn) Send(id string, instr Instruction) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.members[id]
	if !ok {
		return ErrUnknownParticipant
	}
	return instruct(p, instr)
}

// Replay re-delivers the recorded decision to every participant, as a
// crash recovery would. Because instructions are idempotent, replay does
// not change final states or repeat local commit actions.
func (t *Txn) Replay() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.decided {
		return ErrNotDecided
	}
	t.deliverLocked()
	return nil
}

// Query returns a consistent snapshot. Before a decision is recorded the
// Decision field is the zero value; afterwards repeated queries are
// stable.
func (t *Txn) Query() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := Snapshot{Phase: t.phase}
	if t.decided {
		snap.Decision = t.decision
	}
	for _, id := range t.order {
		snap.Members = append(snap.Members, ParticipantStatus{
			ID:    id,
			State: t.members[id].State(),
		})
	}
	return snap
}

// expired reports whether the deadline has been reached. The interval is
// left-closed/right-open: now == deadline already counts as expired.
func (t *Txn) expired(deadline time.Time) bool {
	return !t.now().Before(deadline)
}

func (t *Txn) deliverLocked() {
	instr := InstrAbort
	t.phase = PhaseAbort
	if t.decision.Verdict == vote.VerdictCommit {
		instr = InstrCommit
		t.phase = PhaseCommit
	}
	for _, id := range t.order {
		// Delivery is a consequence of the recorded decision; the
		// state machine makes illegal combinations impossible, so a
		// well-formed run never errors here.
		_ = instruct(t.members[id], instr)
	}
}

func instruct(p *participant.Participant, instr Instruction) error {
	if instr == InstrCommit {
		return p.Commit()
	}
	return p.Abort()
}
