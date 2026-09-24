// Package api is the public entry point of the first-write-wins dedup
// register. The dependency direction is one-way: api -> fww -> reg.
package api

import (
	"errors"
	"fmt"

	"ontology/fww"
)

// Write is one upstream write. Change is one changelog row (Added=false: "-").
type Write = fww.Write
type Change = fww.Change

// Decidable sentinel errors; match with errors.Is.
var (
	ErrEmptyKey       = fww.ErrEmptyKey
	ErrNonPositiveSeq = fww.ErrNonPositiveSeq
	ErrDuplicateSeq   = fww.ErrDuplicateSeq
)

// Register materializes the smallest-Seq value seen so far per key; its view
// can only be maintained by applying the changelog that Feed emits.
type Register struct {
	s *fww.Store
}

// New returns an empty register.
func New() *Register { return &Register{s: fww.New()} }

// Feed applies one batch atomically and returns the changelog it emits. A
// rejected write fails the whole batch and leaves all state untouched.
func (r *Register) Feed(ws []Write) ([]Change, error) { return r.s.Feed(ws) }

// View returns an independent snapshot Key -> effective Val.
func (r *Register) View() map[string]string {
	var v map[string]string
	r.s.Snapshot(func(view map[string]string, _ int64) { v = view })
	return v
}

// Dropped returns the total number of deduplicated late writes.
func (r *Register) Dropped() int64 {
	var d int64
	r.s.Snapshot(func(_ map[string]string, dropped int64) { d = dropped })
	return d
}

// SelfCheck replays a built-in sequence and verifies the four invariants, plus
// the O(1) key-location bound. It returns only an error.
func (r *Register) SelfCheck() error {
	t := New()
	ws := []Write{{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"},
		{Key: "K", Seq: 10, Val: "c"}, {Key: "K", Seq: 1, Val: "d"},
		{Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"},
		{Key: "x", Seq: 2, Val: "p"}}
	var log []Change
	for _, w := range ws {
		c, err := t.Feed([]Write{w})
		if err != nil {
			return err
		}
		log = append(log, c...)
	}
	got, drops := t.View(), t.Dropped()
	// Invariant 1: equals batch recomputation (min Seq per key).
	if len(got) != 2 || got["K"] != "d" || got["x"] != "p" || drops != 3 {
		return fmt.Errorf("api selfcheck: view/dropped mismatch %v %d", got, drops)
	}
	// Invariants 2 and 3: applying every changelog prefix keeps at most one
	// live value per key and each "-" matches that live (Seq,Val).
	type e struct {
		seq int64
		val string
	}
	live, prev := map[string]e{}, map[string]int64{}
	for _, c := range log {
		if c.Added {
			if _, ok := live[c.Key]; ok {
				return errors.New("api selfcheck: two live values for a key")
			}
			if p, ok := prev[c.Key]; ok && c.Seq >= p {
				return errors.New("api selfcheck: effective seq not decreasing")
			}
			live[c.Key], prev[c.Key] = e{c.Seq, c.Val}, c.Seq
			continue
		}
		v, ok := live[c.Key]
		if !ok || v.seq != c.Seq || v.val != c.Val {
			return errors.New("api selfcheck: withdraw does not match the live value")
		}
		delete(live, c.Key)
	}
	if len(live) != len(got) {
		return errors.New("api selfcheck: replayed view disagrees")
	}
	// Invariant 4: a rejected batch leaves no trace; the register stays usable.
	before := fmt.Sprintf("%v/%d/%d", got, drops, len(log))
	if _, err := t.Feed([]Write{{Key: "K", Seq: 1, Val: "z"}}); !errors.Is(err, ErrDuplicateSeq) {
		return fmt.Errorf("api selfcheck: rejection error = %v", err)
	}
	if after := fmt.Sprintf("%v/%d/%d", t.View(), t.Dropped(), len(log)); after != before {
		return errors.New("api selfcheck: rejected batch left a trace")
	}
	if _, err := t.Feed([]Write{{Key: "z", Seq: 9, Val: "ok"}}); err != nil {
		return err
	}
	return fww.LookupCheck()
}
