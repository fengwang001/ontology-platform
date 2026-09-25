package ontology

import (
	"errors"
	"testing"
	"time"
)

// Characterization tests: every assertion in this file pins the CURRENT,
// observed behavior of the implementation, whether or not that behavior
// matches the documented intent. See FINDINGS.md for the mismatches.

// overlapShape describes one overwrite of the base fact "old" [10,20)@tx1
// by "new" [newFrom,newTo)@tx2. A negative newTo means +infinity.
type overlapShape struct {
	name    string
	newFrom int64
	newTo   int64
	// residualPoints are validAt values that after tx2 are covered only by
	// a same-value residual of the base fact (never by "new").
	residualPoints []int64
	// sameTxFacts is how many stored facts carry TxFrom == tx2 after the
	// overwrite (the residuals plus the new fact).
	sameTxFacts int
}

var overlapShapes = []overlapShape{
	{"split-middle", 13, 16, []int64{10, 11, 12, 16, 17, 19}, 3},
	{"left-overlap", 5, 15, []int64{15, 16, 19}, 2},
	{"right-overlap", 15, 25, []int64{10, 11, 14}, 2},
	{"open-ended-new", 15, -1, []int64{10, 11, 14}, 2},
	{"full-cover", 5, 25, nil, 1},
}

func buildOverlapStore(t *testing.T, sh overlapShape) *Store {
	t.Helper()
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	vt := time.Time{}
	if sh.newTo >= 0 {
		vt = ts(sh.newTo)
	}
	mustWrite(t, s, "e", "p", "new", ts(sh.newFrom), vt, ts(2))
	return s
}

// Pin: a pure interval narrowing (same value, smaller valid interval)
// shows up in Corrections as an extra entry, indistinguishable in count
// from a real value correction.
func TestCorrectionsCountsSameValueResiduals(t *testing.T) {
	for _, sh := range overlapShapes {
		if sh.residualPoints == nil {
			continue
		}
		t.Run(sh.name, func(t *testing.T) {
			s := buildOverlapStore(t, sh)
			for _, at := range sh.residualPoints {
				got, err := s.Corrections("e", "p", ts(at))
				if err != nil {
					t.Fatalf("validAt=%d: %v", at, err)
				}
				// The value at this point was "old" before AND after the
				// overwrite: zero real value changes, yet the trajectory
				// holds two entries because the residual is a new fact.
				if len(got) != 2 {
					t.Fatalf("validAt=%d: want 2 entries for a point whose value never changed, got %+v", at, got)
				}
				if got[0].Value != "old" || got[1].Value != "old" {
					t.Errorf("validAt=%d: both entries carry the same value: %+v", at, got)
				}
				if !got[0].TxFrom.Equal(ts(1)) || !got[0].TxTo.Equal(ts(2)) {
					t.Errorf("validAt=%d: entry 0 = %+v, want closed original {old,1,2}", at, got[0])
				}
				if !got[1].TxFrom.Equal(ts(2)) || !got[1].TxTo.IsZero() {
					t.Errorf("validAt=%d: entry 1 = %+v, want live residual {old,2,0}", at, got[1])
				}
			}
		})
	}

	// Contrast: a genuine value change ALSO yields two entries, so entry
	// count alone cannot tell a correction apart from a narrowing.
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(5), ts(25), ts(2))
	got, err := s.Corrections("e", "p", ts(11))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Value != "old" || got[1].Value != "new" {
		t.Errorf("real correction trajectory = %+v, want [{old,1,2} {new,2,0}]", got)
	}
}

// Pin: one overlapping write stamps every fact it creates (residuals and
// the new fact) with the SAME TxFrom, so "strictly increasing TxFrom" is
// violated at the fact level. At any single validAt, however, Corrections
// never contains two entries with equal TxFrom, because same-transaction
// facts have disjoint valid intervals — the documented ordering holds only
// via that undocumented invariant, and sort.SliceStable's tie-handling is
// never exercised.
func TestCorrectionsTxFromCharacterization(t *testing.T) {
	for _, sh := range overlapShapes {
		t.Run(sh.name, func(t *testing.T) {
			s := buildOverlapStore(t, sh)

			count := 0
			for _, f := range s.entity("e").props["p"] {
				if f.TxFrom.Equal(ts(2)) {
					count++
				}
			}
			if count != sh.sameTxFacts {
				t.Errorf("facts with TxFrom==tx2: want %d, got %d", sh.sameTxFacts, count)
			}

			for at := int64(4); at <= 26; at++ {
				got, err := s.Corrections("e", "p", ts(at))
				if errors.Is(err, ErrValidOutOfRange) {
					continue
				}
				if err != nil {
					t.Fatalf("validAt=%d: %v", at, err)
				}
				for i := 1; i < len(got); i++ {
					if !got[i].TxFrom.After(got[i-1].TxFrom) {
						t.Errorf("validAt=%d: TxFrom not strictly increasing: %+v", at, got)
					}
				}
			}
		})
	}
}

// Pin: via the public API, at most one visible fact ever covers a given
// validAt, so AsOf's first-match linear scan is deterministic and does not
// depend on the append order of the facts slice.
func TestAsOfUniqueVisibleFactAndSliceOrder(t *testing.T) {
	type asofResult struct {
		value string
		err   error
	}
	for _, sh := range overlapShapes {
		t.Run(sh.name, func(t *testing.T) {
			s := buildOverlapStore(t, sh)
			facts := s.entity("e").props["p"]

			for at := int64(4); at <= 26; at++ {
				visible := 0
				for _, f := range facts {
					if contains(f.ValidFrom, f.ValidTo, ts(at)) && f.visibleAt(ts(2)) {
						visible++
					}
				}
				if visible > 1 {
					t.Errorf("validAt=%d: %d visible facts cover the same point", at, visible)
				}
			}

			collect := func() map[int64]asofResult {
				res := make(map[int64]asofResult)
				for at := int64(4); at <= 26; at++ {
					f, err := s.AsOf("e", "p", ts(at), ts(2))
					r := asofResult{err: err}
					if err == nil {
						r.value = f.Value.(string)
					}
					res[at] = r
				}
				return res
			}
			before := collect()

			rev := make([]*Fact, len(facts))
			for i, f := range facts {
				rev[len(facts)-1-i] = f
			}
			s.entity("e").props["p"] = rev

			after := collect()
			for at, want := range before {
				if after[at] != want {
					t.Errorf("validAt=%d: slice reversal changed AsOf result: %+v -> %+v", at, want, after[at])
				}
			}
		})
	}
}

// Pin: the uniqueness invariant above is not enforced by AsOf itself.
// Given two visible facts covering the same validAt (a state the public
// API never produces), AsOf returns whichever comes first in the slice —
// the result depends on append order, not on any semantic tie-break.
func TestAsOfReturnsFirstMatchWhenVisibleFactsOverlap(t *testing.T) {
	mk := func(v string, tx int64) *Fact {
		return &Fact{
			Entity: "e", Property: "p", Value: v,
			ValidFrom: ts(10), ValidTo: ts(20), TxFrom: ts(tx),
		}
	}
	orders := []struct {
		name  string
		facts []*Fact
		want  string
	}{
		{"older-tx-first", []*Fact{mk("a", 1), mk("b", 2)}, "a"},
		{"newer-tx-first", []*Fact{mk("b", 2), mk("a", 1)}, "b"},
	}
	for _, tc := range orders {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			s.entity("e").props["p"] = tc.facts
			f, err := s.AsOf("e", "p", ts(15), ts(100))
			if err != nil {
				t.Fatal(err)
			}
			if f.Value != tc.want {
				t.Errorf("AsOf = %q, want %q (first fact in slice order)", f.Value, tc.want)
			}
		})
	}
}

// Pin: validateWrite never compares txAt against the valid interval, so
// "future knowledge" (txAt < validFrom) and arbitrary backfills are all
// accepted and queryable.
func TestWriteIgnoresTxVsValidOrdering(t *testing.T) {
	cases := []struct {
		name      string
		validFrom int64
		validTo   int64 // -1 => +infinity
		tx        int64
		queryAt   int64
	}{
		{"future-knowledge", 100, 200, 1, 150},
		{"far-future-knowledge", 1 << 30, -1, 2, 1 << 30},
		{"tx-equals-validFrom", 10, 20, 10, 15},
		{"tx-inside-valid-interval", 10, 20, 15, 15},
		{"backfill-after-validTo", 10, 20, 30, 15},
		{"deep-past-backfill", 1, 2, 50, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			vt := time.Time{}
			if tc.validTo >= 0 {
				vt = ts(tc.validTo)
			}
			err := s.Write("e", "p", "v", ts(tc.validFrom), vt, ts(tc.tx))
			if err != nil {
				t.Fatalf("write rejected (behavior changed?): %v", err)
			}
			f, err := s.AsOf("e", "p", ts(tc.queryAt), ts(tc.tx))
			if err != nil || f.Value != "v" {
				t.Errorf("accepted write not queryable: %v %v", f, err)
			}
		})
	}
}

// Pin: entity() takes the global s.mu on every call, so any operation —
// even on an already-existing entity, even a read — blocks while the
// global mutex is held. The documented "writes to different entities never
// block each other" only holds at the entity-level es.mu. Observations are
// logged, not hard-asserted; the only hard assertion is that every
// operation completes once the global mutex is released.
func TestEntityLookupSerializesOnGlobalMutex(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "a", "p", "v0", ts(1), ts(10), ts(1))
	mustWrite(t, s, "b", "p", "v0", ts(1), ts(10), ts(1))

	ops := []struct {
		name string
		run  func() error
	}{
		{"write-existing-entity", func() error { return s.Write("a", "p", "v1", ts(1), ts(10), ts(2)) }},
		{"write-other-entity", func() error { return s.Write("b", "p", "v1", ts(1), ts(10), ts(2)) }},
		{"read-existing-entity", func() error { _, err := s.AsOf("a", "p", ts(5), ts(2)); return err }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			s.mu.Lock()
			done := make(chan error, 1)
			go func() { done <- op.run() }()

			blocked := true
			select {
			case err := <-done:
				blocked = false
				t.Logf("UNEXPECTED: %s completed while global mutex held (err=%v)", op.name, err)
			case <-time.After(100 * time.Millisecond):
				t.Logf("observed: %s blocked on global mutex for >=100ms", op.name)
			}
			s.mu.Unlock()

			if blocked {
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("op failed after global mutex released: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("op did not complete after global mutex released")
				}
			}
		})
	}
}
