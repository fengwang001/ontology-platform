package plancache

import (
	"errors"
	"math/big"
	"testing"
)

func nextD(t *testing.T, s *Selector, name string) Decision {
	t.Helper()
	d, err := s.Next(name)
	if err != nil {
		t.Fatalf("Next(%q): unexpected error %v", name, err)
	}
	return d
}

func reportCustom(t *testing.T, s *Selector, name string, cost int64) {
	t.Helper()
	if err := s.ReportCustom(name, cost); err != nil {
		t.Fatalf("ReportCustom(%q,%d): unexpected error %v", name, cost, err)
	}
}

func reportGeneric(t *testing.T, s *Selector, name string, cost int64) {
	t.Helper()
	if err := s.ReportGeneric(name, cost); err != nil {
		t.Fatalf("ReportGeneric(%q,%d): unexpected error %v", name, cost, err)
	}
}

func doneGeneric(t *testing.T, s *Selector, name string) {
	t.Helper()
	if err := s.DoneGeneric(name); err != nil {
		t.Fatalf("DoneGeneric(%q): unexpected error %v", name, err)
	}
}

// At c = K-1 the verdict is still Custom; after the K-th custom report
// (c == K) with g unknown it is BuildGeneric exactly once.
func TestBoundaryKMinus1AndK(t *testing.T) {
	s, err := New(3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare("q"); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 2; i++ {
		if d := nextD(t, s, "q"); d != Custom {
			t.Fatalf("trial %d (c=%d): got %s, want Custom", i, i-1, d)
		}
		reportCustom(t, s, "q", 10)
	}
	// c == 2 == K-1: still Custom.
	if d := nextD(t, s, "q"); d != Custom {
		t.Fatalf("at c=K-1: got %s, want Custom", d)
	}
	reportCustom(t, s, "q", 10)

	// c == K == 3, g unknown: BuildGeneric once.
	if d := nextD(t, s, "q"); d != BuildGeneric {
		t.Fatalf("at c=K, g unknown: got %s, want BuildGeneric", d)
	}
	reportGeneric(t, s, "q", 1)

	// g*c = 3 < sum = 30 -> UseGeneric.
	if d := nextD(t, s, "q"); d != UseGeneric {
		t.Fatalf("cheap generic: got %s, want UseGeneric", d)
	}
	doneGeneric(t, s, "q")
}

// g*c == sum + P*c exactly must stay Custom (strictly-less rule).
func TestEqualityStaysCustom(t *testing.T) {
	s, err := New(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare("q"); err != nil {
		t.Fatal(err)
	}
	if d := nextD(t, s, "q"); d != Custom {
		t.Fatalf("first trial: got %s, want Custom", d)
	}
	reportCustom(t, s, "q", 10) // c=1, sum=10
	if d := nextD(t, s, "q"); d != BuildGeneric {
		t.Fatalf("got %s, want BuildGeneric", d)
	}
	reportGeneric(t, s, "q", 15) // g=15: 15 == 10 + 5*1
	if d := nextD(t, s, "q"); d != Custom {
		t.Fatalf("equality: got %s, want Custom (strict <)", d)
	}
}

// With P the generic total is below the custom total (UseGeneric); dropping P
// flips the identical statistics to Custom.
func TestRemovingPFlipsDecision(t *testing.T) {
	build := func(p int64) *Selector {
		s, err := New(1, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Prepare("q"); err != nil {
			t.Fatal(err)
		}
		nextD(t, s, "q")
		reportCustom(t, s, "q", 100) // c=1, sum=100
		nextD(t, s, "q")
		reportGeneric(t, s, "q", 105) // g=105
		return s
	}

	// P=10: 105 < 100 + 10 -> UseGeneric.
	if d := nextD(t, build(10), "q"); d != UseGeneric {
		t.Fatalf("P=10: got %s, want UseGeneric", d)
	}
	// P=0: 105 < 100 is false -> Custom.
	if d := nextD(t, build(0), "q"); d != Custom {
		t.Fatalf("P=0: got %s, want Custom", d)
	}
}

// UseGeneric + DoneGeneric mutates no statistics and repeated Next keeps
// returning the same decision; ReportCustom changes c/sum and accumulated
// custom costs can flip Custom into UseGeneric.
func TestPendingProtocolAndStats(t *testing.T) {
	s, _ := New(1, 0)
	_ = s.Prepare("q")
	nextD(t, s, "q")
	reportCustom(t, s, "q", 100)
	nextD(t, s, "q")
	reportGeneric(t, s, "q", 10) // g=10, c=1: 10 < 100 -> UseGeneric

	st := s.stmts["q"]
	for i := 0; i < 3; i++ {
		if d := nextD(t, s, "q"); d != UseGeneric {
			t.Fatalf("round %d: got %s, want UseGeneric", i, d)
		}
		doneGeneric(t, s, "q")
		if st.c.Cmp(big.NewInt(1)) != 0 || st.sum.Cmp(big.NewInt(100)) != 0 {
			t.Fatalf("stats changed after DoneGeneric: c=%s sum=%s", st.c, st.sum)
		}
	}

	// Wrong report kind against a UseGeneric pending.
	nextD(t, s, "q")
	if err := s.ReportCustom("q", 1); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("report custom on UseGeneric pending: got %v", err)
	}
	if err := s.ReportGeneric("q", 1); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("report generic on UseGeneric pending: got %v", err)
	}
	doneGeneric(t, s, "q")

	// Custom trials accumulate c/sum: g=90, custom costs 100 each.
	// c=2,sum=200: 180 < 200 flips from Custom to UseGeneric.
	s2, _ := New(2, 0)
	_ = s2.Prepare("flip")
	nextD(t, s2, "flip")
	reportCustom(t, s2, "flip", 100)
	nextD(t, s2, "flip")
	reportCustom(t, s2, "flip", 100)
	nextD(t, s2, "flip")
	reportGeneric(t, s2, "flip", 90)
	if d := nextD(t, s2, "flip"); d != UseGeneric {
		t.Fatalf("accumulated custom costs should flip: got %s, want UseGeneric", d)
	}
	st2 := s2.stmts["flip"]
	if st2.c.Cmp(big.NewInt(2)) != 0 || st2.sum.Cmp(big.NewInt(200)) != 0 {
		t.Fatalf("c=%s sum=%s, want 2/200", st2.c, st2.sum)
	}
	doneGeneric(t, s2, "flip")
}

// Rejected operations never mutate state; in particular an out-of-range cost
// leaves the pending decision in place.
func TestRejectionOrderAndState(t *testing.T) {
	s, _ := New(1, 0)

	if _, err := New(0, 0); !errors.Is(err, ErrKTooSmall) {
		t.Fatalf("K=0: got %v", err)
	}
	if _, err := New(0, -1); !errors.Is(err, ErrKTooSmall) {
		t.Fatalf("K=0,P=-1 must report K first: got %v", err)
	}
	if _, err := New(1, -1); !errors.Is(err, ErrPNegative) {
		t.Fatalf("P=-1: got %v", err)
	}

	if err := s.Prepare(""); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name: got %v", err)
	}
	_ = s.Prepare("q")
	if err := s.Prepare("q"); !errors.Is(err, ErrNameExists) {
		t.Fatalf("dup prepare: got %v", err)
	}

	if _, err := s.Next("nope"); !errors.Is(err, ErrStatementNotFound) {
		t.Fatalf("next missing: got %v", err)
	}
	nextD(t, s, "q") // Custom pending
	if _, err := s.Next("q"); !errors.Is(err, ErrPendingExists) {
		t.Fatalf("double next: got %v", err)
	}

	if err := s.ReportCustom("nope", 1); !errors.Is(err, ErrStatementNotFound) {
		t.Fatalf("report missing: got %v", err)
	}
	if err := s.ReportGeneric("q", 1); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("generic while custom pending: got %v", err)
	}
	if err := s.DoneGeneric("q"); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("done while custom pending: got %v", err)
	}
	if err := s.ReportCustom("q", -1); !errors.Is(err, ErrCostOutOfRange) {
		t.Fatalf("cost -1: got %v", err)
	}
	if err := s.ReportCustom("q", MaxCost+1); !errors.Is(err, ErrCostOutOfRange) {
		t.Fatalf("cost 2^40+1: got %v", err)
	}
	st := s.stmts["q"]
	if st.pending != Custom || st.c.Sign() != 0 || st.sum.Sign() != 0 {
		t.Fatalf("state mutated by rejected report: pending=%d c=%s sum=%s",
			st.pending, st.c, st.sum)
	}
	reportCustom(t, s, "q", MaxCost) // 2^40 is legal

	if err := s.ReportCustom("q", 1); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("report without pending: got %v", err)
	}
	if err := s.DoneGeneric("q"); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("done without pending: got %v", err)
	}

	nextD(t, s, "q") // BuildGeneric pending
	if err := s.ReportCustom("q", 1); !errors.Is(err, ErrPendingMismatch) {
		t.Fatalf("custom while buildgeneric pending: got %v", err)
	}
	if err := s.ReportGeneric("q", MaxCost+1); !errors.Is(err, ErrCostOutOfRange) {
		t.Fatalf("generic cost oob: got %v", err)
	}
	if st.pending != BuildGeneric {
		t.Fatalf("pending lost after rejected generic report: %d", st.pending)
	}
	reportGeneric(t, s, "q", MaxCost)

	if err := s.Bump(0); !errors.Is(err, ErrVersionNotGreater) {
		t.Fatalf("bump 0: got %v", err)
	}
	if err := s.Drop("nope"); !errors.Is(err, ErrStatementNotFound) {
		t.Fatalf("drop missing: got %v", err)
	}
}

// After Bump, older statements re-trial K times; statements registered at the
// new version are untouched by a bump to that same version.
func TestBumpResetsOldOnly(t *testing.T) {
	s, _ := New(2, 0)
	_ = s.Prepare("old")
	nextD(t, s, "old")
	reportCustom(t, s, "old", 7)

	if err := s.Bump(3); err != nil {
		t.Fatal(err)
	}
	old := s.stmts["old"]
	if old.registeredVersion != 3 || old.c.Sign() != 0 || old.sum.Sign() != 0 ||
		old.g != nil || old.pending >= 0 {
		t.Fatalf("old statement not reset: %+v", old)
	}
	for i := 0; i < 2; i++ {
		if d := nextD(t, s, "old"); d != Custom {
			t.Fatalf("re-trial %d: got %s, want Custom", i, d)
		}
		reportCustom(t, s, "old", 1)
	}

	_ = s.Prepare("fresh") // registered at version 3
	nextD(t, s, "fresh")
	reportCustom(t, s, "fresh", 5)
	fresh := s.stmts["fresh"]
	if fresh.c.Cmp(big.NewInt(1)) != 0 || fresh.sum.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("new-version statement lost stats: c=%s sum=%s", fresh.c, fresh.sum)
	}

	if err := s.Bump(4); err != nil {
		t.Fatal(err)
	}
	if fresh.registeredVersion != 4 || fresh.c.Sign() != 0 || fresh.g != nil ||
		fresh.pending >= 0 {
		t.Fatalf("version-3 statement not reset at bump 4: %+v", fresh)
	}

	// Drop really removes; re-Prepare starts fresh at version 4.
	if err := s.Drop("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Next("old"); !errors.Is(err, ErrStatementNotFound) {
		t.Fatalf("next after drop: got %v", err)
	}
}

// cost 2^40 with c beyond 2^20 must compare without overflow.
func TestNoOverflowLargeValues(t *testing.T) {
	const trials = 1<<20 + 5
	s, _ := New(1, 0)
	_ = s.Prepare("big")
	nextD(t, s, "big")
	reportCustom(t, s, "big", MaxCost)
	nextD(t, s, "big")
	reportGeneric(t, s, "big", MaxCost)

	st := s.stmts["big"]
	// g*c == sum (2^40 each) -> equality -> Custom. Repeat custom trials all
	// at max cost; equality always holds regardless of c.
	for i := 0; i < trials-1; i++ {
		if d := nextD(t, s, "big"); d != Custom {
			t.Fatalf("iteration %d: got %s, want Custom at equality", i, d)
		}
		reportCustom(t, s, "big", MaxCost)
	}
	want := new(big.Int).Mul(big.NewInt(MaxCost), big.NewInt(trials))
	if st.sum.Cmp(want) != 0 {
		t.Fatalf("sum=%s, want %s", st.sum, want)
	}
	if st.c.Cmp(big.NewInt(trials)) != 0 {
		t.Fatalf("c=%s, want %d", st.c, trials)
	}

	// A generic one unit cheaper flips strictly: g*c < sum.
	s2, _ := New(trials, 0)
	_ = s2.Prepare("big2")
	for i := 0; i < trials; i++ {
		nextD(t, s2, "big2")
		reportCustom(t, s2, "big2", MaxCost)
	}
	nextD(t, s2, "big2")
	cheap := MaxCost - 1
	reportGeneric(t, s2, "big2", cheap)
	if d := nextD(t, s2, "big2"); d != UseGeneric {
		t.Fatalf("cheaper generic at c>2^20: got %s, want UseGeneric", d)
	}
}
