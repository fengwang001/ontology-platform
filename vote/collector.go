package vote

import (
	"errors"
	"sync"
)

var (
	// ErrUnknownParticipant is returned when a ballot or timeout is
	// recorded for an ID that was never registered.
	ErrUnknownParticipant = errors.New("vote: unknown participant")
	// ErrDuplicateParticipant is returned when the same ID is
	// registered twice.
	ErrDuplicateParticipant = errors.New("vote: duplicate participant")
	// ErrAlreadyAccounted is returned when a participant already has a
	// recorded ballot or timeout.
	ErrAlreadyAccounted = errors.New("vote: participant already accounted")
	// ErrDecided is returned when a ballot or timeout arrives after the
	// decision has been finalized. The decision is never changed by it.
	ErrDecided = errors.New("vote: decision already finalized")
)

// Collector gathers ballots and timeouts for a fixed set of participant
// IDs and finalizes a Decision once every participant is accounted for.
// It is safe for concurrent use.
//
// Ruling rules:
//   - any reject  -> abort with ReasonRejected (rejects win over timeouts)
//   - else any timeout -> abort with ReasonTimeout
//   - else (all agreed, including the empty set) -> commit
//
// The culprit is the first matching participant in registration order.
type Collector struct {
	mu        sync.Mutex
	order     []string
	known     map[string]bool
	ballots   map[string]Ballot
	timedOut  map[string]bool
	remaining int
	decided   bool
	decision  Decision
}

// NewCollector creates a Collector expecting exactly the given IDs.
// Duplicate IDs are rejected. An empty ID set finalizes immediately
// with a commit decision.
func NewCollector(ids []string) (*Collector, error) {
	c := &Collector{
		known:    make(map[string]bool, len(ids)),
		ballots:  make(map[string]Ballot, len(ids)),
		timedOut: make(map[string]bool, len(ids)),
	}
	for _, id := range ids {
		if c.known[id] {
			return nil, ErrDuplicateParticipant
		}
		c.known[id] = true
		c.order = append(c.order, id)
	}
	c.remaining = len(ids)
	c.finalizeLocked()
	return c, nil
}

// Cast records a ballot for a participant.
func (c *Collector) Cast(id string, b Ballot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.known[id] {
		return ErrUnknownParticipant
	}
	if c.decided {
		return ErrDecided
	}
	if c.accountedLocked(id) {
		return ErrAlreadyAccounted
	}
	c.ballots[id] = b
	c.remaining--
	c.finalizeLocked()
	return nil
}

// Timeout marks a participant as not having answered before the
// deadline. A timeout counts as a reject for the verdict, but is
// reported with ReasonTimeout.
func (c *Collector) Timeout(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.known[id] {
		return ErrUnknownParticipant
	}
	if c.decided {
		return ErrDecided
	}
	if c.accountedLocked(id) {
		return ErrAlreadyAccounted
	}
	c.timedOut[id] = true
	c.remaining--
	c.finalizeLocked()
	return nil
}

// Decision returns the finalized decision. The second return value is
// false while participants are still unaccounted for, in which case the
// returned Decision is the zero value.
func (c *Collector) Decision() (Decision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decision, c.decided
}

func (c *Collector) accountedLocked(id string) bool {
	if _, ok := c.ballots[id]; ok {
		return true
	}
	return c.timedOut[id]
}

func (c *Collector) finalizeLocked() {
	if c.decided || c.remaining > 0 {
		return
	}
	for i := len(c.order) - 1; i >= 0; i-- {
		id := c.order[i]
		if !c.timedOut[id] && c.ballots[id] == BallotReject {
			c.decision = Decision{Verdict: VerdictAbort, Reason: ReasonRejected, Culprit: id}
			c.decided = true
			return
		}
	}
	for _, id := range c.order {
		if c.timedOut[id] {
			c.decision = Decision{Verdict: VerdictAbort, Reason: ReasonTimeout, Culprit: id}
			c.decided = true
			return
		}
	}
	c.decision = Decision{Verdict: VerdictCommit, Reason: ReasonAllAgreed}
	c.decided = true
}
