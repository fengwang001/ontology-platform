// Package api is the public facade of the incremental FULL OUTER JOIN engine.
package api

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"ontology/foj"
	"ontology/rel"
)

type (
	Op     = foj.Op     // one input operation
	Change = rel.Change // one change-log entry
	Row    = rel.Row    // one materialized view row
)

const ( // operation kinds
	PutLeft  = foj.PutLeft
	PutRight = foj.PutRight
	DelLeft  = foj.DelLeft
	DelRight = foj.DelRight
)

type API struct{ eng *foj.Engine }

func New() *API                                  { return &API{eng: foj.New()} }
func (a *API) Apply(ops ...Op) ([]Change, error) { return a.eng.Apply(ops...) }
func (a *API) View() []Row                       { return a.eng.View() }

func mk(kind foj.Kind, key, id string) Op { return Op{Kind: kind, Key: key, ID: id} }

type pair struct{ L, R map[string]bool } // left/right row-id sets of one key (reference model)
type refState map[string]*pair

// refApply applies op to the reference, returning side sizes before/after.
func refApply(s refState, op Op) (l0, r0, l1, r1 int) {
	p := s[op.Key]
	if p == nil {
		p = &pair{map[string]bool{}, map[string]bool{}}
		s[op.Key] = p
	}
	l0, r0 = len(p.L), len(p.R)
	mine := p.L
	if op.Kind == PutRight || op.Kind == DelRight {
		mine = p.R
	}
	if op.Kind == PutLeft || op.Kind == PutRight {
		mine[op.ID] = true
	} else {
		delete(mine, op.ID)
	}
	return l0, r0, len(p.L), len(p.R)
}

// refView recomputes the view from the reference multisets (batch semantics);
// rel.Rel.View's own expansion is pinned by the eight-step demo check.
func refView(s refState) []Row {
	var out []Row
	for _, k := range slices.Sorted(maps.Keys(s)) {
		r := rel.New(k)
		for id := range s[k].L {
			r.PutLeft(id)
		}
		for id := range s[k].R {
			r.PutRight(id)
		}
		out = append(out, r.View()...)
	}
	return out
}

// checkSeq replays seq, verifying invariants 1-3 after every op.
func checkSeq(seq []Op) error {
	eng, ref, shadow := foj.New(), refState{}, map[Row]bool{}
	for i, op := range seq {
		cs, err := eng.Apply(op)
		if err != nil {
			return fmt.Errorf("op %d: %w", i, err)
		}
		l0, r0, l1, r1 := refApply(ref, op)
		cross := (l0 == 0) != (l1 == 0) || (r0 == 0) != (r1 == 0)
		mt, pt := -1, -1 // row types retracted/inserted: 0=pair, 1=NULL-padding
		for _, c := range cs {
			if shadow[c.Row] == c.Add { // re-insert of live / retract of absent
				return fmt.Errorf("op %d: bad change %v", i, c.Row)
			}
			shadow[c.Row] = c.Add
			t := 0
			if c.Lid == rel.Null || c.Rid == rel.Null {
				t = 1
			}
			if c.Add {
				pt = t
			} else {
				mt = t
			}
		}
		if !cross && mt >= 0 && pt >= 0 && mt != pt {
			return fmt.Errorf("op %d: padding/pair switch without 0-crossing", i)
		}
		if got, want := eng.View(), refView(ref); !slices.Equal(got, want) {
			return fmt.Errorf("op %d: view %v != recompute %v", i, got, want)
		}
	}
	return nil
}

// SelfCheck verifies the four invariants on the built-in sequences.
func (a *API) SelfCheck() error {
	seqs := [][]Op{{mk(PutLeft, "K", "a"), mk(PutRight, "K", "x"), mk(PutRight, "K", "y"), mk(PutLeft, "K", "b"), // the NOTES.md eight-step sequence
		mk(DelLeft, "K", "a"), mk(DelLeft, "K", "b"), mk(DelRight, "K", "x"), mk(DelRight, "K", "y")}}
	var mix []Op
	for i := 0; i < 24; i++ {
		j, kind := i, PutLeft
		if i >= 12 {
			j, kind = 23-i, DelLeft
		}
		k := string(rune('A' + j%3))
		mix = append(mix, mk(kind, k, string(rune('a'+j))), mk(kind+1, k, string(rune('a'+j))))
	}
	for i, s := range append(seqs, mix) {
		if err := checkSeq(s); err != nil {
			return fmt.Errorf("seq %d: %w", i, err)
		}
	}
	b := New() // invariant 4: rejected ops leave no trace
	_, _ = b.Apply(mk(PutLeft, "K", "a"), mk(PutRight, "K", "x"))
	snap := b.View()
	badOps := []Op{mk(PutLeft, "K", "a"), mk(DelRight, "K", "zz"), mk(PutLeft, "", "b")} // dup id, missing id, empty key
	badErrs := []error{rel.ErrDuplicate, rel.ErrNotFound, foj.ErrEmptyKey}
	for i, op := range badOps {
		if _, err := b.Apply(op); !errors.Is(err, badErrs[i]) {
			return fmt.Errorf("bad op %d: got %v", i, err)
		}
		if j := (i + 1) % 3; errors.Is(badErrs[i], badErrs[j]) { // sentinels must be distinct
			return fmt.Errorf("errors %d,%d not distinct", i, j)
		}
	}
	if _, err := b.Apply(mk(PutLeft, "K", "b"), mk(DelLeft, "K", "nope")); !errors.Is(err, rel.ErrNotFound) {
		return fmt.Errorf("batch: got %v", err)
	}
	if !slices.Equal(b.View(), snap) {
		return fmt.Errorf("state changed after rejected ops: %v", b.View())
	}
	_, err := b.Apply(mk(PutLeft, "K", "b")) // still usable
	return err
}
