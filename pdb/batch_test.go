package pdb

import "testing"

// TestBatchAllOrNothing verifies cumulative allowance consumption across one
// budget, batch-start semantics for unready pods, duplicates, and that
// rejection mutates nothing.
func TestBatchAllOrNothing(t *testing.T) {
	lg := newOpLogger(t)
	s := New(60 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(2)}, at(0)), "budget minAv=2")
	// 3 ready + 1 unready => allowance 1.
	for _, u := range []string{"a", "b", "c"} {
		must(t, s.UpsertPod(mkPod("ns", u, true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "ready "+u)
	}
	must(t, s.UpsertPod(mkPod("ns", "u", false, PhaseRunning, map[string]string{"app": "x"}), at(2)), "unready u")

	// Two ready pods in one batch need 2 > allowance 1: all rejected.
	err := s.EvictBatch([]PodRef{{"ns", "a"}, {"ns", "b"}}, at(3))
	lg.log("batch [a,b] need 2 allowance 1 => %s", errStr(err))
	wantReason(t, err, ReasonInsufficient)
	if de, ok := err.(*DecisionError); !ok || de.Index != 1 {
		t.Fatalf("offending index = %d want 1", de.Index)
	}
	st, _ := s.Budget("ns", "b", at(4))
	if st.CurrentReady != 3 {
		t.Fatalf("rejected batch must not change state, ready=%d", st.CurrentReady)
	}

	// One ready + unready: ready consumes 1, unready judged at batch-start
	// ready=3 >= 2: all allowed.
	must(t, s.EvictBatch([]PodRef{{"ns", "a"}, {"ns", "u"}}, at(5)), "batch mixed")
	lg.log("batch [a(ready),u(unready)] => ALLOW; ready drops to 2, u does not consume")
	st2, _ := s.Budget("ns", "b", at(6))
	if st2.CurrentReady != 2 || st2.DisruptionAllowed != 0 {
		t.Fatalf("post-batch ready=2 allowance=0, got %+v", st2)
	}

	// Duplicate ref: invalid argument, reported at its position.
	err = s.EvictBatch([]PodRef{{"ns", "b"}, {"ns", "c"}, {"ns", "b"}}, at(7))
	lg.log("batch with duplicate b => %s", errStr(err))
	wantReason(t, err, ReasonInvalidArgument)
	if de := err.(*DecisionError); de.Index != 2 {
		t.Fatalf("duplicate index = %d want 2", de.Index)
	}

	// Empty/illegal ref reported at its position.
	err = s.EvictBatch([]PodRef{{"ns", "b"}, {"", "x"}}, at(8))
	wantReason(t, err, ReasonInvalidArgument)
	if de := err.(*DecisionError); de.Index != 1 {
		t.Fatalf("illegal ref index = %d want 1", de.Index)
	}
	lg.log("batch with empty-namespace ref at index 1 => InvalidArgument")
}

// TestBatchUnreadyBatchStartSemantics: unready pod gating uses batch-start
// state, never state changed by earlier ready pods in the same batch.
func TestBatchUnreadyBatchStartSemantics(t *testing.T) {
	lg := newOpLogger(t)
	s := New(60 * 1e9)
	// minAvailable=2: four ready pods plus one unready (expected=5) give
	// allowance 2.
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(2)}, at(0)), "minAv=2")
	must(t, s.UpsertPod(mkPod("ns", "r1", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "r1")
	must(t, s.UpsertPod(mkPod("ns", "r2", true, PhaseRunning, map[string]string{"app": "x"}), at(2)), "r2")
	must(t, s.UpsertPod(mkPod("ns", "r3", true, PhaseRunning, map[string]string{"app": "x"}), at(3)), "r3")
	must(t, s.UpsertPod(mkPod("ns", "r4", true, PhaseRunning, map[string]string{"app": "x"}), at(4)), "r4")
	must(t, s.UpsertPod(mkPod("ns", "u", false, PhasePending, map[string]string{"app": "x"}), at(5)), "u")

	// Three ready + u would need 3 allowance but only 2 exists, proving
	// cumulative consumption; [r1,r2,u] consumes exactly 2 and u's gate reads
	// batch-start ready=4 >= 2 even though post-consumption ready=2.
	err := s.EvictBatch([]PodRef{{"ns", "r1"}, {"ns", "r2"}, {"ns", "r3"}, {"ns", "u"}}, at(6))
	lg.log("batch [r1,r2,r3,u] needs 3 allowance of 2 => %s", errStr(err))
	wantReason(t, err, ReasonInsufficient)
	if de := err.(*DecisionError); de.Index != 2 {
		t.Fatalf("offending index=%d want 2", de.Index)
	}
	st0, _ := s.Budget("ns", "b", at(7))
	if st0.CurrentReady != 4 {
		t.Fatalf("rejected batch changes nothing, ready=%d", st0.CurrentReady)
	}

	must(t, s.EvictBatch([]PodRef{{"ns", "r1"}, {"ns", "r2"}, {"ns", "u"}}, at(8)), "mixed batch")
	lg.log("batch [r1,r2(consume 2),u(gate at batch-start ready=4>=2)] => ALLOW")
	st, _ := s.Budget("ns", "b", at(9))
	if st.CurrentReady != 2 {
		t.Fatalf("r1,r2 consumed, ready=%d want 2", st.CurrentReady)
	}
	ps, _ := s.InspectPod(PodRef{"ns", "u"})
	if !ps.Evicting || ps.Pod.Ready {
		t.Fatal("u must be evicting and remain unready")
	}
}

// TestErrorPriority ensures only the highest-priority reason is reported.
func TestErrorPriority(t *testing.T) {
	lg := newOpLogger(t)

	// Invalid argument beats clock rollback.
	s := New(60 * 1e9)
	must(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning, nil), at(10)), "pod")
	wantReason(t, s.Evict(PodRef{"ns", ""}, at(5)), ReasonInvalidArgument)
	lg.log("empty uid with rollback time => InvalidArgument (not ClockRollback)")

	// Clock rollback beats not-found.
	wantReason(t, s.Evict(PodRef{"ns", "ghost"}, at(9)), ReasonClockRollback)
	lg.log("missing pod at rolled-back time => ClockRollback (not PodNotFound)")

	// Not-found beats phase/conflict/allowance: a Succeeded pod that is also
	// missing reports PodNotFound.
	wantReason(t, s.Evict(PodRef{"ns", "ghost"}, at(11)), ReasonPodNotFound)
	lg.log("missing pod => PodNotFound (not phase/conflict/allowance)")

	// Phase beats already-evicting? Pod can't be both terminal and in-flight,
	// but a terminal pod that matches two budgets reports phase first.
	s2 := New(60 * 1e9)
	must(t, s2.UpsertBudget(BudgetSpec{Name: "b1", Namespace: "n",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(0)), "b1")
	must(t, s2.UpsertBudget(BudgetSpec{Name: "b2", Namespace: "n",
		Selector: Selector{"app": "x", "t": "1"}, MinAvailable: minAv(0)}, at(1)), "b2")
	must(t, s2.UpsertPod(mkPod("n", "term", true, PhaseFailed,
		map[string]string{"app": "x", "t": "1"}), at(2)), "terminal+conflict")
	wantReason(t, s2.Evict(PodRef{"n", "term"}, at(3)), ReasonNotEvictablePhase)
	lg.log("terminal pod matching 2 budgets => NotEvictablePhase (before Conflict)")

	// Already-evicting beats conflict and allowance: a pod matching two
	// budgets is evicted once while the second budget briefly does not match
	// (so the first eviction is accepted), then b2 is made to match again.
	must(t, s2.UpsertPod(mkPod("n", "live", true, PhaseRunning,
		map[string]string{"app": "x"}), at(4)), "live matches only b1")
	must(t, s2.Evict(PodRef{"n", "live"}, at(5)), "evict live")
	must(t, s2.UpsertBudget(BudgetSpec{Name: "b2", Namespace: "n",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(6)), "b2 now also matches")
	wantReason(t, s2.Evict(PodRef{"n", "live"}, at(7)), ReasonAlreadyEvicting)
	lg.log("evicting pod that newly matches 2 budgets => AlreadyEvicting (before Conflict)")
}

// TestBudgetMutationEffects verifies immediate effects of budget changes.
func TestBudgetMutationEffects(t *testing.T) {
	lg := newOpLogger(t)
	s := New(60 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(0)), "minAv=0")
	must(t, s.UpsertPod(mkPod("ns", "p", true, PhaseRunning, map[string]string{"app": "x"}), at(1)), "p")
	must(t, s.Evict(PodRef{"ns", "p"}, at(2)), "evict with minAv=0")
	lg.log("evict p at t=2 under minAv=0 => ALLOW (in flight)")

	// Tightening the budget above current ready must not touch the in-flight
	// eviction but must deny later ready evictions.
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(5)}, at(3)), "minAv=5")
	must(t, s.UpsertPod(mkPod("ns", "q", true, PhaseRunning, map[string]string{"app": "x"}), at(4)), "q")
	err := s.Evict(PodRef{"ns", "q"}, at(5))
	lg.log("evict q after required 5 > current ready 1 => %s", errStr(err))
	wantReason(t, err, ReasonInsufficient)

	// In-flight eviction still completes normally.
	must(t, s.Confirm(PodRef{"ns", "p"}, at(6)), "confirm p unaffected")
	lg.log("in-flight eviction of p confirms unaffected by budget tightening")

	// Relaxing the budget reopens allowance.
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(7)), "minAv=0 again")
	must(t, s.Evict(PodRef{"ns", "q"}, at(8)), "evict q after relax")
	lg.log("evict q after budget relaxed => ALLOW")

	// Invalid budget definitions are rejected and change nothing.
	err = s.UpsertBudget(BudgetSpec{Name: "bad", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(1), MaxUnavailable: maxUn(1)}, at(9))
	wantReason(t, err, ReasonInvalidArgument)
	err = s.UpsertBudget(BudgetSpec{Name: "bad", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minPct(101)}, at(10))
	wantReason(t, err, ReasonInvalidArgument)
	err = s.UpsertBudget(BudgetSpec{Name: "bad", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: &Value{Amount: -1}}, at(11))
	wantReason(t, err, ReasonInvalidArgument)
	lg.log("both-set / 101%% / negative values => InvalidArgument")
}
