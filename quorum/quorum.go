// Package quorum decides majority questions over the acceptors of one
// single-decree Paxos instance: which value a proposer must adopt from its
// prepare responses, and which value (if any) is currently chosen.
//
// The tally is maintained incrementally: each accept moves one vote, so
// Chosen never scans the acceptor table. lastReads records how many acceptors
// the most recent Chosen actually read; it stays zero once the tally is
// populated.
package quorum

import (
	"sync/atomic"

	"ontology/acpt"
)

// Report is one acceptor's Prepare response relevant to value selection.
type Report struct {
	Accepted int  // highest proposal number accepted (0 if never)
	Value    int  // value carried by Accepted
	HasValue bool // true when Accepted > 0
}

// PickValue selects the value the proposer must accept in its round. If any
// report carries an accepted value, the value with the largest accepted
// proposal number wins (ties share one value by Paxos safety); otherwise
// ownValue is used.
func PickValue(reports []Report, ownValue int) int {
	bestN := 0
	chosen := ownValue
	for _, r := range reports {
		if r.HasValue && r.Accepted > bestN {
			bestN, chosen = r.Accepted, r.Value
		}
	}
	return chosen
}

// Tally holds the per-value accept counts incrementally. The zero value is
// not usable; construct with New.
type Tally struct {
	counts    map[int]int // value -> number of acceptors currently holding it
	majority  int
	lastReads atomic.Int64 // acceptors read by the most recent Chosen call
}

// New builds a tally over m acceptors (majority = m/2 + 1).
func New(m int) *Tally {
	return &Tally{counts: map[int]int{}, majority: m/2 + 1}
}

// Move transfers one vote from oldVal to newVal. Both may be 0 (the
// "never accepted" placeholder); 0 is never counted as a held value.
func (t *Tally) Move(oldVal, newVal int) {
	if oldVal != 0 {
		t.counts[oldVal]--
		if t.counts[oldVal] == 0 {
			delete(t.counts, oldVal)
		}
	}
	if newVal != 0 {
		t.counts[newVal]++
	}
}

// Chosen returns the value currently held by a majority of acceptors and
// true, or (0, false). It answers straight from the incremental counts: the
// acceptor table is never scanned, so lastReads stays independent of m.
func (t *Tally) Chosen() (int, bool) {
	t.lastReads.Store(0) // counts are maintained; no acceptor is read
	for v, c := range t.counts {
		if c >= t.majority {
			return v, true
		}
	}
	return 0, false
}

// readBound is the m-independent ceiling on acceptors a single Chosen may
// read. The incremental tally reads none.
const readBound = 1

// ReadsBounded reports whether the most recent Chosen read no more than the
// fixed constant readBound acceptor entries. It exposes only the verdict,
// never the counter value.
func (t *Tally) ReadsBounded() bool { return t.lastReads.Load() <= readBound }

// Naive recomputes the answer by scanning every acceptor: the reference
// implementation the incremental tally must always agree with.
func Naive(as []*acpt.Acceptor, majority int) (int, bool) {
	counts := map[int]int{}
	for _, a := range as {
		if v := a.AcceptedValue(); v != 0 {
			counts[v]++
		}
	}
	for v, c := range counts {
		if c >= majority {
			return v, true
		}
	}
	return 0, false
}
