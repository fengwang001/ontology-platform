package ontology

import (
	"errors"
	"testing"
	"time"
)

// Characterization tests for under-specified edge semantics of the
// bitemporal store. Every assertion pins the CURRENT observable behavior
// (not the behavior the comments promise); see FINDINGS.md.

// carveShape is one overlap geometry between an old and a new fact.
type carveShape struct {
	name        string
	oldFrom     int64
	oldTo       int64 // zero means +infinity
	newFrom     int64
	newTo       int64 // zero means +infinity
	probeAt     int64 // a point landing on a surviving residual (same value as old)
	residuals   int   // residual facts the carve is expected to produce
	sameTxCount int   // stored facts carrying the new write's TxFrom
}

func carveShapes() []carveShape {
	return []carveShape{
		{name: "middle", oldFrom: 10, oldTo: 20, newFrom: 13, newTo: 16, probeAt: 11, residuals: 2, sameTxCount: 3},
		{name: "left-only", oldFrom: 10, oldTo: 20, newFrom: 5, newTo: 15, probeAt: 17, residuals: 1, sameTxCount: 2},
		{name: "right-only", oldFrom: 10, oldTo: 20, newFrom: 15, newTo: 25, probeAt: 12, residuals: 1, sameTxCount: 2},
		{name: "infinite-old", oldFrom: 10, oldTo: 0, newFrom: 15, newTo: 20, probeAt: 12, residuals: 2, sameTxCount: 3},
	}
}

func buildShape(t *testing.T, sh carveShape) *Store {
	t.Helper()
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(sh.oldFrom), tsOrInf(sh.oldTo), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(sh.newFrom), tsOrInf(sh.newTo), ts(2))
	return s
}

func tsOrInf(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return ts(sec)
}

func distinctValues(cs []Correction) int {
	seen := map[any]bool{}
	for _, c := range cs {
		seen[c.Value] = true
	}
	return len(seen)
}

// 1) Narrowing a valid interval (carving a same-value residual) is recorded
// as an extra entry in the correction "trajectory of believed values".
func TestCharacterizeCorrectionsCountNarrowingAsCorrection(t *testing.T) {
	for _, sh := range carveShapes() {
		t.Run(sh.name, func(t *testing.T) {
			s := buildShape(t, sh)
			got, err := s.Corrections("e", "p", ts(sh.probeAt))
			if err != nil {
				t.Fatalf("corrections: %v", err)
			}
			distinct := distinctValues(got)
			// Current behavior: the closed original and the residual both
			// carry "old", so the trajectory has more entries than distinct
			// values, with two identical entries adjacent.
			if len(got) <= distinct {
				t.Fatalf("entries=%d distinct values=%d: expected narrowing to add a duplicate-value entry", len(got), distinct)
			}
			adjacentDup := false
			for i := 1; i < len(got); i++ {
				if got[i].Value == got[i-1].Value {
					adjacentDup = true
				}
			}
			if !adjacentDup {
				t.Errorf("no adjacent identical-value entries: %+v", got)
			}
			t.Logf("%s: Corrections at %d = %d entries but only %d distinct value(s): %+v",
				sh.name, sh.probeAt, len(got), distinct, got)
		})
	}
}

// 2) One overlapping write stores multiple facts with the SAME TxFrom
// (new fact + residual(s), all TxFrom = txAt). The "strictly increasing
// TxFrom" ordering is only satisfied because such facts never co-cover a
// single validAt; the sort itself does not enforce strictness on ties.
func TestCharacterizeCorrectionsDuplicateTxFrom(t *testing.T) {
	for _, sh := range carveShapes() {
		t.Run(sh.name+"/storage", func(t *testing.T) {
			s := buildShape(t, sh)
			facts := s.entity("e").props["p"]
			sameTx := 0
			for _, f := range facts {
				if f.TxFrom.Equal(ts(2)) {
					sameTx++
				}
			}
			if sameTx != sh.sameTxCount {
				t.Fatalf("%s: want %d stored facts with TxFrom=2 (new+residuals), got %d", sh.name, sh.sameTxCount, sameTx)
			}
			if sh.residuals == 0 {
				t.Fatalf("shape %s expected at least one residual", sh.name)
			}
			t.Logf("%s: one write produced %d facts sharing TxFrom=2 (new fact + %d residual(s))",
				sh.name, sameTx, sh.residuals)
		})

		t.Run(sh.name+"/output", func(t *testing.T) {
			s := buildShape(t, sh)
			got, err := s.Corrections("e", "p", ts(sh.probeAt))
			if err != nil {
				t.Fatalf("corrections: %v", err)
			}
			strict := true
			for i := 1; i < len(got); i++ {
				if !got[i].TxFrom.After(got[i-1].TxFrom) {
					strict = false
				}
			}
			// Current behavior: at a single validAt the shared-TxFrom facts
			// never co-occur (new interval and residuals are disjoint), so the
			// output is strictly increasing by accident of the data layout.
			if !strict {
				t.Fatalf("Write-generated output unexpectedly non-strict: %+v", got)
			}
			t.Logf("%s: Corrections at %d is strictly increasing only because same-TxFrom facts are disjoint: %+v",
				sh.name, sh.probeAt, got)
		})
	}

	// If equal-TxFrom facts DO cover the same point (state not reachable via
	// Write, but the ordering layer has no defense against it), SliceStable
	// keeps raw slice order and the "strictly increasing" property breaks.
	crafted := []struct {
		name  string
		order []any
		want  any
	}{
		{"a-before-b", []any{"a", "b"}, "a"},
		{"b-before-a", []any{"b", "a"}, "b"},
	}
	for _, c := range crafted {
		t.Run("tie-order/"+c.name, func(t *testing.T) {
			s := NewStore()
			facts := make([]*Fact, 0, 2)
			for _, v := range c.order {
				facts = append(facts, &Fact{Entity: "e", Property: "p", Value: v,
					ValidFrom: ts(0), ValidTo: ts(20), TxFrom: ts(5)})
			}
			s.entity("e").props["p"] = facts
			got, err := s.Corrections("e", "p", ts(10))
			if err != nil {
				t.Fatalf("corrections: %v", err)
			}
			if got[0].Value != c.want || !got[0].TxFrom.Equal(got[1].TxFrom) {
				t.Fatalf("tie ordering: got %+v, want first value %v with equal TxFrom", got, c.want)
			}
			t.Logf("equal TxFrom tie resolved by raw append order: first=%v", got[0].Value)
		})
	}
}

// 3) AsOf linearly scans and returns the FIRST visible covering fact. With
// Write-generated state that never matters (at most one visible fact covers
// a point), but with multiple visible facts the result is slice-order
// dependent.
func TestCharacterizeAsOfFirstVisibleIsSliceOrderDependent(t *testing.T) {
	crafted := []struct {
		name  string
		order []any
		want  any
	}{
		{"a-before-b", []any{"a", "b"}, "a"},
		{"b-before-a", []any{"b", "a"}, "b"},
	}
	for _, c := range crafted {
		t.Run(c.name, func(t *testing.T) {
			s := NewStore()
			facts := make([]*Fact, 0, 2)
			for _, v := range c.order {
				facts = append(facts, &Fact{Entity: "e", Property: "p", Value: v,
					ValidFrom: ts(0), ValidTo: ts(20), TxFrom: ts(1)})
			}
			s.entity("e").props["p"] = facts
			f, err := s.AsOf("e", "p", ts(10), ts(9))
			if err != nil || f.Value != c.want {
				t.Fatalf("AsOf = %v (%v), want first-in-slice %v", f.Value, err, c.want)
			}
			t.Logf("multiple visible facts: AsOf returned first slice element %q", c.want)
		})
	}

	// Over Write-generated histories the dependency is latent: enumerate
	// every (shape, validAt, txAt) and show reversing the internal facts
	// slice leaves the AsOf answer unchanged.
	for _, sh := range carveShapes() {
		t.Run("write-invariant/"+sh.name, func(t *testing.T) {
			s := buildShape(t, sh)
			es := s.entity("e")
			reverse := func() {
				for i, j := 0, len(es.props["p"])-1; i < j; i, j = i+1, j-1 {
					es.props["p"][i], es.props["p"][j] = es.props["p"][j], es.props["p"][i]
				}
			}
			for va := int64(-2); va <= 30; va++ {
				for tx := int64(0); tx <= 4; tx++ {
					f1, e1 := s.AsOf("e", "p", ts(va), ts(tx))
					reverse()
					f2, e2 := s.AsOf("e", "p", ts(va), ts(tx))
					reverse() // restore original order
					if !errors.Is(e1, e2) || (e1 == nil && f1.Value != f2.Value) {
						t.Fatalf("AsOf changed under slice reversal at va=%d tx=%d: %v/%v vs %v/%v",
							va, tx, f1.Value, e1, f2.Value, e2)
					}
				}
			}
		})
	}
}

// 4) validateWrite never relates txAt to the valid interval: future
// knowledge (txAt < validFrom) and arbitrary backfill are both accepted.
func TestCharacterizeWriteAcceptsFutureKnowledgeAndBackfill(t *testing.T) {
	cases := []struct {
		name    string
		vf, vt  int64
		tx      int64
		probeVa int64
		probeTx int64
		value   any
		wantErr error
	}{
		{name: "future-known-before-valid", vf: 100, vt: 200, tx: 1, probeVa: 150, probeTx: 1, value: "v"},
		{name: "future-known-outside-range", vf: 100, vt: 200, tx: 1, probeVa: 99, probeTx: 1, value: "v", wantErr: ErrValidOutOfRange},
		{name: "backfill-not-known-before-tx", vf: 1, vt: 10, tx: 100, probeVa: 5, probeTx: 50, value: "v", wantErr: ErrNotYetKnown},
		{name: "backfill-visible-at-tx", vf: 1, vt: 10, tx: 100, probeVa: 5, probeTx: 100, value: "v"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewStore()
			if err := s.Write("e", "p", c.value, ts(c.vf), ts(c.vt), ts(c.tx)); err != nil {
				t.Fatalf("write accepted semantics: unexpected rejection %v", err)
			}
			f, err := s.AsOf("e", "p", ts(c.probeVa), ts(c.probeTx))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("AsOf err = %v, want %v", err, c.wantErr)
				}
				t.Logf("%s: accepted; AsOf(va=%d,tx=%d) -> %v", c.name, c.probeVa, c.probeTx, err)
				return
			}
			if err != nil || f.Value != c.value {
				t.Fatalf("AsOf = %v (%v), want %v", f.Value, err, c.value)
			}
			t.Logf("%s: accepted; fact valid in [%d,%d) already visible at tx=%d", c.name, c.vf, c.vt, c.tx)
		})
	}
}

// 5) entity() always takes the global s.mu first, even for existing
// entities, so reads and writes of different entities serialize on the
// global lock. This is a timing observation: the worker must not finish
// while the global lock is held, and finishes promptly after release.
func TestCharacterizeGlobalLockSerializesExistingEntities(t *testing.T) {
	ops := []struct {
		name string
		op   func(s *Store)
	}{
		{"write", func(s *Store) {
			if err := s.Write("b", "p", 1, ts(10), ts(20), ts(100)); err != nil {
				t.Errorf("write: %v", err)
			}
		}},
		{"asof", func(s *Store) { _, _ = s.AsOf("b", "p", ts(15), ts(100)) }},
		{"corrections", func(s *Store) { _, _ = s.Corrections("b", "p", ts(15)) }},
	}
	const hold = 50 * time.Millisecond
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			s := NewStore()
			mustWrite(t, s, "a", "p", 1, ts(10), ts(20), ts(1))
			mustWrite(t, s, "b", "p", 1, ts(10), ts(20), ts(1)) // ensure entities exist

			done := make(chan struct{})
			s.mu.Lock()
			go func() {
				op.op(s)
				close(done)
			}()
			time.Sleep(hold)
			select {
			case <-done:
				t.Fatalf("%s on existing entity completed while global s.mu was held", op.name)
			default:
			}
			s.mu.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("%s did not complete after global lock release", op.name)
			}
			t.Logf("%s on a different existing entity was blocked behind s.mu for ~%v (per-entity lock alone was free)",
				op.name, hold)
		})
	}
}
