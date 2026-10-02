package plancache

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

func mustNew(t *testing.T, k, p int64) *Selector {
	t.Helper()
	s, err := New(k, p)
	if err != nil {
		t.Fatalf("New(%d, %d): unexpected error: %v", k, p, err)
	}
	return s
}

func mustPrepare(t *testing.T, s *Selector, name string) {
	t.Helper()
	if err := s.Prepare(name); err != nil {
		t.Fatalf("Prepare(%q): unexpected error: %v", name, err)
	}
}

func mustNext(t *testing.T, s *Selector, name string) Decision {
	t.Helper()
	d, err := s.Next(name)
	if err != nil {
		t.Fatalf("Next(%q): unexpected error: %v", name, err)
	}
	return d
}

func mustReportCustom(t *testing.T, s *Selector, name string, cost uint64) {
	t.Helper()
	if err := s.ReportCustom(name, cost); err != nil {
		t.Fatalf("ReportCustom(%q, %d): unexpected error: %v", name, cost, err)
	}
}

func mustReportGeneric(t *testing.T, s *Selector, name string, cost uint64) {
	t.Helper()
	if err := s.ReportGeneric(name, cost); err != nil {
		t.Fatalf("ReportGeneric(%q, %d): unexpected error: %v", name, cost, err)
	}
}

func checkReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with reason %v, got nil", want)
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *plancache.Error, got %T: %v", err, err)
	}
	if pe.Reason != want {
		t.Fatalf("expected reason %v, got %v (%v)", want, pe.Reason, err)
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New(0, 0); err == nil {
		t.Fatal("New(0, 0): expected error")
	} else {
		checkReason(t, err, ReasonInvalidK)
	}
	if _, err := New(-3, 0); err == nil {
		t.Fatal("New(-3, 0): expected error")
	} else {
		checkReason(t, err, ReasonInvalidK)
	}
	if _, err := New(1, -1); err == nil {
		t.Fatal("New(1, -1): expected error")
	} else {
		checkReason(t, err, ReasonNegativeP)
	}
	// Both invalid: report K first.
	if _, err := New(0, -1); err == nil {
		t.Fatal("New(0, -1): expected error")
	} else {
		checkReason(t, err, ReasonInvalidK)
	}
	if _, err := New(1, 0); err != nil {
		t.Fatalf("New(1, 0): unexpected error: %v", err)
	}
}

func TestPrepareValidation(t *testing.T) {
	s := mustNew(t, 1, 0)
	checkReason(t, s.Prepare(""), ReasonEmptyName)
	mustPrepare(t, s, "a")
	checkReason(t, s.Prepare("a"), ReasonAlreadyExists)
	// Empty name takes precedence over already-exists.
	checkReason(t, s.Prepare(""), ReasonEmptyName)
	// Rejected prepares must not change state.
	if _, ok := s.StatsOf(""); ok {
		t.Fatal("rejected Prepare(\"\") created a statement")
	}
}

// TestTrialPhaseDecisions covers c == K-1 and c == K.
func TestTrialPhaseDecisions(t *testing.T) {
	const k = 3
	s := mustNew(t, k, 0)
	mustPrepare(t, s, "q")
	for i := 0; i < k; i++ {
		st, _ := s.StatsOf("q")
		if st.C != uint64(i) {
			t.Fatalf("before trial %d: c = %d, want %d", i, st.C, i)
		}
		if d := mustNext(t, s, "q"); d != Custom {
			t.Fatalf("trial %d (c=%d): got %v, want Custom", i, i, d)
		}
		mustReportCustom(t, s, "q", 10)
	}
	// c == K now: trial phase is over, generic plan is unknown.
	if d := mustNext(t, s, "q"); d != BuildGeneric {
		t.Fatalf("at c==K: got %v, want BuildGeneric", d)
	}
}

// TestExactlyOneBuildGeneric verifies BuildGeneric is returned exactly once
// after the K-th custom execution.
func TestExactlyOneBuildGeneric(t *testing.T) {
	s := mustNew(t, 2, 0)
	mustPrepare(t, s, "q")
	for i := 0; i < 2; i++ {
		if d := mustNext(t, s, "q"); d != Custom {
			t.Fatalf("trial %d: got %v, want Custom", i, d)
		}
		mustReportCustom(t, s, "q", 10)
	}
	// The K-th custom is done: exactly one BuildGeneric follows.
	if d := mustNext(t, s, "q"); d != BuildGeneric {
		t.Fatalf("after K customs: got %v, want BuildGeneric", d)
	}
	mustReportGeneric(t, s, "q", 100)
	// From now on the decision is Custom or UseGeneric, never BuildGeneric.
	for i := 0; i < 8; i++ {
		d := mustNext(t, s, "q")
		if d == BuildGeneric {
			t.Fatalf("round %d after ReportGeneric: got BuildGeneric again", i)
		}
		if d == UseGeneric {
			if err := s.DoneGeneric("q"); err != nil {
				t.Fatalf("DoneGeneric: %v", err)
			}
		} else {
			mustReportCustom(t, s, "q", 10)
		}
	}
}

func TestReportWithoutPending(t *testing.T) {
	s := mustNew(t, 2, 0)
	mustPrepare(t, s, "q")
	checkReason(t, s.ReportCustom("q", 1), ReasonNoPending)
	checkReason(t, s.ReportGeneric("q", 1), ReasonNoPending)
	checkReason(t, s.DoneGeneric("q"), ReasonNoPending)
	checkReason(t, s.ReportCustom("missing", 1), ReasonNotFound)
	checkReason(t, s.ReportGeneric("missing", 1), ReasonNotFound)
	checkReason(t, s.DoneGeneric("missing"), ReasonNotFound)
}

// setupCompared builds a statement with c=1, sum=customCost, g=genericCost.
func setupCompared(t *testing.T, s *Selector, name string, customCost, genericCost uint64) {
	t.Helper()
	mustPrepare(t, s, name)
	if d := mustNext(t, s, name); d != Custom {
		t.Fatalf("first Next: got %v, want Custom", d)
	}
	mustReportCustom(t, s, name, customCost)
	if d := mustNext(t, s, name); d != BuildGeneric {
		t.Fatalf("second Next: got %v, want BuildGeneric", d)
	}
	mustReportGeneric(t, s, name, genericCost)
}

// TestEqualityBoundaryChoosesCustom: g*c == sum + P*c must still pick Custom
// (the rule is strict less-than).
func TestEqualityBoundaryChoosesCustom(t *testing.T) {
	// c=1, sum=5, P=10, g=15: g*c = 15, sum+P*c = 15 -> equal -> Custom.
	s := mustNew(t, 1, 10)
	setupCompared(t, s, "q", 5, 15)
	if d := mustNext(t, s, "q"); d != Custom {
		t.Fatalf("g*c == sum+P*c: got %v, want Custom", d)
	}
	mustReportCustom(t, s, "q", 0)
	// c=2, sum=5, g=15: g*c=30 > sum+P*c=25 -> still Custom.
	if d := mustNext(t, s, "q"); d != Custom {
		t.Fatalf("g*c > sum+P*c: got %v, want Custom", d)
	}
}

// TestOverheadFlipsDecision: the same statistics yield different decisions
// with and without the planning overhead P.
func TestOverheadFlipsDecision(t *testing.T) {
	// c=1, sum=5, g=12.
	// P=0:  g*c=12 < sum+P*c=5  -> false -> Custom.
	// P=10: g*c=12 < sum+P*c=15 -> true  -> UseGeneric.
	without := mustNew(t, 1, 0)
	setupCompared(t, without, "q", 5, 12)
	if d := mustNext(t, without, "q"); d != Custom {
		t.Fatalf("P=0: got %v, want Custom", d)
	}
	with := mustNew(t, 1, 10)
	setupCompared(t, with, "q", 5, 12)
	if d := mustNext(t, with, "q"); d != UseGeneric {
		t.Fatalf("P=10: got %v, want UseGeneric", d)
	}
}

// TestUseGenericDoneKeepsStats: UseGeneric + DoneGeneric changes no
// statistics, so the next Next repeats the same decision.
func TestUseGenericDoneKeepsStats(t *testing.T) {
	s := mustNew(t, 1, 10)
	setupCompared(t, s, "q", 5, 12)
	before, _ := s.StatsOf("q")
	for round := 0; round < 3; round++ {
		if d := mustNext(t, s, "q"); d != UseGeneric {
			t.Fatalf("round %d: got %v, want UseGeneric", round, d)
		}
		if err := s.DoneGeneric("q"); err != nil {
			t.Fatalf("DoneGeneric: %v", err)
		}
		after, _ := s.StatsOf("q")
		if after.C != before.C || after.Sum.Cmp(before.Sum) != 0 ||
			after.G != before.G || after.HasG != before.HasG {
			t.Fatalf("round %d: stats changed: before=%+v after=%+v", round, before, after)
		}
	}
}

// TestCustomReportsFlipToUseGeneric: while the decision is Custom, each
// ReportCustom changes c and sum, and the decision can flip to UseGeneric.
func TestCustomReportsFlipToUseGeneric(t *testing.T) {
	// K=1, P=0, g=10. First custom costs 20: g*c=10 < sum=20 false -> Custom.
	s := mustNew(t, 1, 0)
	setupCompared(t, s, "q", 20, 10)
	prev, _ := s.StatsOf("q")
	if d := mustNext(t, s, "q"); d != Custom {
		t.Fatalf("got %v, want Custom", d)
	}
	mustReportCustom(t, s, "q", 100)
	now, _ := s.StatsOf("q")
	if now.C != prev.C+1 {
		t.Fatalf("c did not increase: %d -> %d", prev.C, now.C)
	}
	wantSum := new(big.Int).Add(prev.Sum, big.NewInt(100))
	if now.Sum.Cmp(wantSum) != 0 {
		t.Fatalf("sum = %v, want %v", now.Sum, wantSum)
	}
	// c=2, sum=120: g*c=20 < 120 -> UseGeneric.
	if d := mustNext(t, s, "q"); d != UseGeneric {
		t.Fatalf("after expensive custom: got %v, want UseGeneric", d)
	}
}

// TestBumpResetsOldStatements: after Bump, statements registered at an older
// version restart the K custom trials from scratch; statements registered
// at the new version are untouched by that bump.
func TestBumpResetsOldStatements(t *testing.T) {
	s := mustNew(t, 2, 0)
	mustPrepare(t, s, "old")
	// Drive "old" past the trial phase and give it a generic plan.
	for i := 0; i < 2; i++ {
		mustNext(t, s, "old")
		mustReportCustom(t, s, "old", 10)
	}
	mustNext(t, s, "old")
	mustReportGeneric(t, s, "old", 100)

	if err := s.Bump(1); err != nil {
		t.Fatalf("Bump(1): %v", err)
	}
	st, _ := s.StatsOf("old")
	if st.C != 0 || st.Sum.Sign() != 0 || st.HasG || st.Version != 1 {
		t.Fatalf("old not reset: %+v", st)
	}
	// Old statement restarts the K custom trials.
	for i := 0; i < 2; i++ {
		if d := mustNext(t, s, "old"); d != Custom {
			t.Fatalf("post-bump trial %d: got %v, want Custom", i, d)
		}
		mustReportCustom(t, s, "old", 10)
	}
	if d := mustNext(t, s, "old"); d != BuildGeneric {
		t.Fatalf("post-bump after K trials: got %v, want BuildGeneric", d)
	}
	mustReportGeneric(t, s, "old", 100)

	// A statement prepared at the new version is unaffected by that bump
	// and accumulates statistics normally.
	mustPrepare(t, s, "new")
	mustNext(t, s, "new")
	mustReportCustom(t, s, "new", 7)
	stNew, _ := s.StatsOf("new")
	if stNew.Version != 1 || stNew.C != 1 || stNew.Sum.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("new statement stats wrong: %+v", stNew)
	}

	// Bump(2) resets both (both registered at version 1 < 2).
	if err := s.Bump(2); err != nil {
		t.Fatalf("Bump(2): %v", err)
	}
	for _, name := range []string{"old", "new"} {
		st, _ := s.StatsOf(name)
		if st.C != 0 || st.Sum.Sign() != 0 || st.HasG || st.Version != 2 {
			t.Fatalf("%s not reset by Bump(2): %+v", name, st)
		}
	}
}

// TestBumpClearsPending: Bump clears a pending decision on reset statements.
func TestBumpClearsPending(t *testing.T) {
	s := mustNew(t, 1, 0)
	mustPrepare(t, s, "q")
	mustNext(t, s, "q") // pending Custom
	if err := s.Bump(1); err != nil {
		t.Fatalf("Bump: %v", err)
	}
	// Pending was cleared: Next succeeds again instead of reporting pending.
	if d := mustNext(t, s, "q"); d != Custom {
		t.Fatalf("got %v, want Custom", d)
	}
	// And the stale report for the pre-bump decision is rejected.
	checkReason(t, s.ReportGeneric("q", 1), ReasonWrongPendingKind)
}

func TestBumpVersionMonotonic(t *testing.T) {
	s := mustNew(t, 1, 0)
	checkReason(t, s.Bump(0), ReasonVersionNotIncreasing)
	if err := s.Bump(5); err != nil {
		t.Fatalf("Bump(5): %v", err)
	}
	checkReason(t, s.Bump(5), ReasonVersionNotIncreasing)
	checkReason(t, s.Bump(4), ReasonVersionNotIncreasing)
	// Rejected bumps leave the version unchanged.
	if err := s.Bump(6); err != nil {
		t.Fatalf("Bump(6): %v", err)
	}
}

func TestCostRange(t *testing.T) {
	s := mustNew(t, 1, 0)
	mustPrepare(t, s, "q")
	mustNext(t, s, "q") // pending Custom
	// cost = 2^40 + 1 is rejected and the pending marker is kept.
	checkReason(t, s.ReportCustom("q", MaxCost+1), ReasonCostOutOfRange)
	_, err := s.Next("q")
	checkReason(t, err, ReasonPendingExists)
	// cost = 2^40 is accepted.
	mustReportCustom(t, s, "q", MaxCost)
	st, _ := s.StatsOf("q")
	if st.C != 1 || st.Sum.Cmp(new(big.Int).SetUint64(MaxCost)) != 0 {
		t.Fatalf("stats after max cost: %+v", st)
	}
	// Same for ReportGeneric.
	mustNext(t, s, "q") // BuildGeneric
	checkReason(t, s.ReportGeneric("q", MaxCost+1), ReasonCostOutOfRange)
	_, err = s.Next("q")
	checkReason(t, err, ReasonPendingExists)
	mustReportGeneric(t, s, "q", MaxCost)
	// Zero cost is valid.
	mustNext(t, s, "q") // Custom (g*c == sum here: 2^40 == 2^40)
	mustReportCustom(t, s, "q", 0)
	// Not-found takes precedence over cost range.
	checkReason(t, s.ReportCustom("missing", MaxCost+1), ReasonNotFound)
	checkReason(t, s.ReportGeneric("missing", MaxCost+1), ReasonNotFound)
}

func TestReportKindMismatch(t *testing.T) {
	s := mustNew(t, 1, 0)
	mustPrepare(t, s, "q")
	mustNext(t, s, "q") // pending Custom
	checkReason(t, s.ReportGeneric("q", 1), ReasonWrongPendingKind)
	checkReason(t, s.DoneGeneric("q"), ReasonWrongPendingKind)
	mustReportCustom(t, s, "q", 1)
	mustNext(t, s, "q") // pending BuildGeneric
	checkReason(t, s.ReportCustom("q", 1), ReasonWrongPendingKind)
	checkReason(t, s.DoneGeneric("q"), ReasonWrongPendingKind)
	mustReportGeneric(t, s, "q", 100)
	// Force a UseGeneric pending: c=1, sum=1, g=100, P=0 -> 100 < 1 false...
	// Use another statement with cheap generic instead.
	s2 := mustNew(t, 1, 0)
	mustPrepare(t, s2, "u")
	mustNext(t, s2, "u")
	mustReportCustom(t, s2, "u", 100)
	mustNext(t, s2, "u")
	mustReportGeneric(t, s2, "u", 1) // g=1 < sum=100 -> UseGeneric
	if d := mustNext(t, s2, "u"); d != UseGeneric {
		t.Fatalf("got %v, want UseGeneric", d)
	}
	checkReason(t, s2.ReportCustom("u", 1), ReasonWrongPendingKind)
	checkReason(t, s2.ReportGeneric("u", 1), ReasonWrongPendingKind)
	if err := s2.DoneGeneric("u"); err != nil {
		t.Fatalf("DoneGeneric: %v", err)
	}
}

func TestDrop(t *testing.T) {
	s := mustNew(t, 1, 0)
	checkReason(t, s.Drop("missing"), ReasonNotFound)
	mustPrepare(t, s, "q")
	mustNext(t, s, "q")
	mustReportCustom(t, s, "q", 5)
	if err := s.Drop("q"); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	checkReason(t, s.Drop("q"), ReasonNotFound)
	_, err := s.Next("q")
	checkReason(t, err, ReasonNotFound)
	// Re-preparing starts from scratch.
	mustPrepare(t, s, "q")
	st, _ := s.StatsOf("q")
	if st.C != 0 || st.Sum.Sign() != 0 || st.HasG {
		t.Fatalf("re-prepared statement not fresh: %+v", st)
	}
}
