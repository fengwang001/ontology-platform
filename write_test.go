package ontology

import (
	"errors"
	"testing"
	"time"
)

func findFact(t *testing.T, facts []*Fact, vf, vt time.Time) *Fact {
	t.Helper()
	for _, f := range facts {
		if f.ValidFrom.Equal(vf) && f.ValidTo.Equal(vt) {
			return f
		}
	}
	return nil
}

func TestSplitMiddleProducesTwoExactResiduals(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(13), ts(16), ts(2))

	facts := s.entity("e").props["p"]
	if len(facts) != 4 {
		t.Fatalf("want 4 facts (original, 2 residuals, new), got %d", len(facts))
	}

	orig := findFact(t, facts, ts(10), ts(20))
	if orig == nil {
		t.Fatal("original fact must be kept, not deleted")
	}
	if !orig.TxTo.Equal(ts(2)) {
		t.Errorf("original TxTo = %v, want %v (closed at write time)", orig.TxTo, ts(2))
	}
	if orig.Value != "old" || !orig.TxFrom.Equal(ts(1)) {
		t.Errorf("original fact must not be modified: %+v", orig)
	}

	left := findFact(t, facts, ts(10), ts(13))
	right := findFact(t, facts, ts(16), ts(20))
	if left == nil || right == nil {
		t.Fatalf("residuals [10,13) and [16,20) missing: %+v", facts)
	}
	for _, r := range []*Fact{left, right} {
		if r.Value != "old" || !r.TxFrom.Equal(ts(2)) || !r.TxTo.IsZero() {
			t.Errorf("bad residual: %+v", r)
		}
	}

	// No off-by-one at any edge of the split.
	cases := []struct {
		at    int64
		value string
	}{
		{10, "old"}, {12, "old"}, {13, "new"}, {15, "new"}, {16, "old"}, {19, "old"},
	}
	for _, c := range cases {
		f, err := s.AsOf("e", "p", ts(c.at), ts(2))
		if err != nil || f.Value != c.value {
			t.Errorf("AsOf(validAt=%d) = %v, %v; want %q", c.at, f.Value, err, c.value)
		}
	}
}

func TestPartialOverlaps(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(5), ts(15), ts(2)) // right residual only

	facts := s.entity("e").props["p"]
	if r := findFact(t, facts, ts(15), ts(20)); r == nil {
		t.Errorf("right residual [15,20) missing: %+v", facts)
	}
	if r := findFact(t, facts, ts(10), ts(15)); r != nil {
		t.Errorf("unexpected extra residual: %+v", r)
	}

	s2 := NewStore()
	mustWrite(t, s2, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s2, "e", "p", "new", ts(15), ts(25), ts(2)) // left residual only
	facts2 := s2.entity("e").props["p"]
	if r := findFact(t, facts2, ts(10), ts(15)); r == nil {
		t.Errorf("left residual [10,15) missing: %+v", facts2)
	}
}

func TestFullCoverLeavesNoResidual(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(0), ts(30), ts(2))

	facts := s.entity("e").props["p"]
	if len(facts) != 2 {
		t.Fatalf("want 2 facts (closed original + new), got %d: %+v", len(facts), facts)
	}
	if _, err := s.AsOf("e", "p", ts(15), ts(2)); err != nil {
		t.Errorf("covered point must hit new fact: %v", err)
	}
}

func TestInfiniteOldIntervalCarved(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), time.Time{}, ts(1))
	mustWrite(t, s, "e", "p", "new", ts(15), ts(20), ts(2))

	facts := s.entity("e").props["p"]
	if r := findFact(t, facts, ts(10), ts(15)); r == nil {
		t.Errorf("left residual [10,15) missing: %+v", facts)
	}
	r := findFact(t, facts, ts(20), time.Time{})
	if r == nil {
		t.Fatalf("infinite right residual [20,+inf) missing: %+v", facts)
	}
	if f, err := s.AsOf("e", "p", ts(1<<40), ts(2)); err != nil || f.Value != "old" {
		t.Errorf("far future must hit infinite residual: %v %v", f, err)
	}
}

func TestTxMonotonicityEnforced(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "v1", ts(10), ts(20), ts(5))

	if err := s.Write("e", "p", "v2", ts(10), ts(20), ts(4)); !errors.Is(err, ErrTxRegression) {
		t.Errorf("regressing tx: want ErrTxRegression, got %v", err)
	}
	if err := s.Write("e", "p", "v2", ts(10), ts(20), ts(5)); !errors.Is(err, ErrTxRegression) {
		t.Errorf("equal tx: want ErrTxRegression, got %v", err)
	}

	f, err := s.AsOf("e", "p", ts(15), ts(100))
	if err != nil || f.Value != "v1" {
		t.Errorf("rejected writes must not change state: %v %v", f.Value, err)
	}
	if got := len(s.entity("e").props["p"]); got != 1 {
		t.Errorf("rejected writes must not add facts, got %d", got)
	}

	// Monotonicity is per entity: another entity may use any tx.
	mustWrite(t, s, "other", "p", "x", ts(10), ts(20), ts(1))
}
