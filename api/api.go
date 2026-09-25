// Package api is the public facade of the append-only audit log.
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/ent"
	"ontology/log"
)

// Audit is the public handle to one audit log.
type Audit struct{ l *log.Log }

// New returns an audit log containing only the genesis entry.
func New() *Audit { return &Audit{l: log.New()} }

// Append appends one entry and returns the assigned Seq.
func (a *Audit) Append(ts int64, who, op string) (int64, error) {
	return a.l.Append(ts, who, op)
}

// Entries returns a copy of all entries, genesis first.
func (a *Audit) Entries() []ent.Entry { return a.l.Entries() }

// Verify returns the Seq of the first tampered entry, or -1.
func (a *Audit) Verify() int64 { return a.l.Verify() }

// Affected returns the Seq of every entry from seq to the end.
func (a *Audit) Affected(seq int64) []int64 { return a.l.Affected(seq) }

// SelfCheck runs a built-in operation sequence and verifies the four
// invariants: ordering, naive-recompute agreement, hash-chain consistency,
// and rejections leaving no trace. It returns nil iff all hold.
func (a *Audit) SelfCheck() error {
	fresh := New()
	steps := []struct {
		ts   int64
		who  string
		op   string
		want int64 // -1 means reject
	}{
		{10, "A", "put", 1}, {12, "A", "get", 2}, {12, "B", "del", 3},
		{9, "A", "put", -1}, {15, "B", "put", 4}, {15, "", "get", -1},
		{20, "A", "del", 5},
	}
	for i, s := range steps {
		seq, err := fresh.Append(s.ts, s.who, s.op)
		if s.want < 0 && err == nil {
			return fmt.Errorf("selfcheck step %d: want rejection", i)
		}
		if s.want >= 0 && (err != nil || seq != s.want) {
			return fmt.Errorf("selfcheck step %d: got seq=%d err=%v", i, seq, err)
		}
	}
	if n := len(fresh.Entries()); n != 6 { // invariant 1 & 4: no gaps, no trace
		return fmt.Errorf("selfcheck: %d entries, want 6", n)
	}
	if v := fresh.Verify(); v != -1 { // invariant 2 & 3: clean chain
		return fmt.Errorf("selfcheck: verify=%d, want -1", v)
	}
	es := fresh.Entries()
	es[3].Op = "put" // tamper Seq=3 without touching stored hashes
	t := log.NewFrom(es)
	if v := t.Verify(); v != 3 {
		return fmt.Errorf("selfcheck: tamper verify=%d, want 3", v)
	}
	if got := t.Affected(3); !slices.Equal(got, []int64{3, 4, 5}) {
		return fmt.Errorf("selfcheck: affected=%v, want [3 4 5]", got)
	}
	errs := []error{log.ErrNegativeTS, log.ErrEmptyWho, log.ErrEmptyOp, log.ErrOutOfOrder}
	for i := range errs { // the four sentinel errors must be distinguishable
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				return fmt.Errorf("selfcheck: errors %d and %d not distinct", i, j)
			}
		}
	}
	return nil
}
