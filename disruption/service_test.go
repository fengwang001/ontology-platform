package disruption

import (
	"testing"
)

// TestEmptySelectorBudgetAdmitsAll: a budget with an empty selector matches
// no pod, so every pod is "unmatched" and admitted.
func TestEmptySelectorBudgetAdmitsAll(t *testing.T) {
	log := newOpLogger(t)
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{}, MinAvailable: abs(100)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		id := PodID{"ns", "p" + string(rune('a'+i))}
		if err := s.UpsertPod(0, pod("ns", id.Name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.BudgetQuota(0, b.ID)
	if err != nil || st.Expected != 0 {
		t.Fatalf("empty selector must expect 0 pods, got %+v err=%v", st, err)
	}
	for i := 0; i < 5; i++ {
		id := PodID{"ns", "p" + string(rune('a'+i))}
		if _, err := s.Evict(1, id, 10); err != nil {
			t.Fatalf("unmatched pod must be admitted: %v", err)
		}
		log.logf("Evict input=%v => allowed, basis: empty-selector budget matches no pod", id)
	}
}

func TestTerminalPhasesExcludedAndRejected(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for _, ph := range []Phase{PhaseSucceeded, PhaseFailed} {
		id := PodID{"ns", ph.String()}
		if err := s.UpsertPod(0, pod("ns", id.Name, ph, false, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
		_, err := s.Evict(0, id, 1)
		mustKind(t, err, KindNotEvictablePhase)
	}
	st, _ := s.BudgetQuota(0, b.ID)
	if st.Expected != 0 {
		t.Fatalf("terminal pods must not count, expected=0 got %d", st.Expected)
	}
}

func TestConflictMultipleBudgets(t *testing.T) {
	s := NewService()
	b1 := PodDisruptionBudget{ID: BudgetID{"ns", "b1"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)}
	b2 := PodDisruptionBudget{ID: BudgetID{"ns", "b2"}, Selector: Selector{"app": "x"}, MaxUnavail: abs(10)}
	if err := s.UpsertBudget(0, b1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBudget(0, b2); err != nil {
		t.Fatal(err)
	}
	id := PodID{"ns", "p"}
	if err := s.UpsertPod(0, pod("ns", "p", PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	_, err := s.Evict(0, id, 1)
	mustKind(t, err, KindConflict)
	// Budget in another namespace must not conflict and must not match.
	s2 := NewService()
	if err := s2.UpsertBudget(0, b1); err != nil {
		t.Fatal(err)
	}
	if err := s2.UpsertPod(0, pod("other", "p", PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Evict(0, PodID{"other", "p"}, 1); err != nil {
		t.Fatalf("same labels in another namespace must be unmatched: %v", err)
	}
}

func TestUnreadyAdmissionRule(t *testing.T) {
	log := newOpLogger(t)
	s := NewService()
	// minAvailable=2 out of 3: required=2.
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"r1", "r2"} {
		if err := s.UpsertPod(0, pod("ns", name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertPod(0, pod("ns", "u", PhaseRunning, false, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	// ready=2 >= required=2: unready admitted, consumes nothing.
	if _, err := s.Evict(1, PodID{"ns", "u"}, 100); err != nil {
		t.Fatalf("unready pod with invariant holding must be admitted: %v", err)
	}
	log.logf("Evict unready input=u ready=2 required=2 => allowed, basis: unready never consumes; currentReady>=required")
	st, _ := s.BudgetQuota(2, b.ID)
	if st.CurrentReady != 2 || st.DisruptionAllowed != 0 {
		t.Fatalf("unready admission must not consume, got %+v", st)
	}
	// Drop one ready pod below required via a stricter budget edit: ready 2
	// vs required 3 => subsequent unready admission rejected.
	strict := PodDisruptionBudget{ID: b.ID, Selector: b.Selector, MinAvailable: abs(3)}
	if err := s.UpsertBudget(3, strict); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(4, PodID{"ns", "u"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Evict(5, PodID{"ns", "u"}, 100)
	mustKind(t, err, KindInsufficient)
	log.logf("Evict unready input=u ready=2 required=3 => rejected Insufficient, basis: currentReady<required at query time")
}

func TestReadyConsumesAndAlreadyEvicting(t *testing.T) {
	log := newOpLogger(t)
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := s.UpsertPod(0, pod("ns", name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	id := PodID{"ns", "a"}
	if _, err := s.Evict(1, id, 100); err != nil {
		t.Fatal(err)
	}
	st, _ := s.BudgetQuota(2, b.ID)
	if st.CurrentReady != 2 || st.DisruptionAllowed != 0 {
		t.Fatalf("admission must consume immediately, got %+v", st)
	}
	log.logf("Evict input=a => allowed, quota afterwards expected=3 ready=2 required=2 allowance=0")
	// Repeat evict: AlreadyEvicting.
	_, err := s.Evict(2, id, 100)
	mustKind(t, err, KindAlreadyEvicting)
	// Readiness mutation while evicting: AlreadyEvicting, state unchanged.
	err = s.SetReady(3, id, false)
	mustKind(t, err, KindAlreadyEvicting)
	if ready, _ := s.PodReady(id); !ready {
		t.Fatal("rejected readiness change must not mutate the pod")
	}
	// Ready pod eviction now: allowance zero -> Insufficient.
	_, err = s.Evict(4, PodID{"ns", "b"}, 100)
	mustKind(t, err, KindInsufficient)
}

func TestExpiryLeftClosedBoundary(t *testing.T) {
	log := newOpLogger(t)
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := s.UpsertPod(0, pod("ns", name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	id := PodID{"ns", "a"}
	// Admit at t=10 with grace 5: deadline 15.
	if _, err := s.Evict(10, id, 5); err != nil {
		t.Fatal(err)
	}
	// t=14: still evicting.
	if _, err := s.Expire(14); err != nil {
		t.Fatal(err)
	}
	if !s.IsEvicting(id) {
		t.Fatal("must still be evicting one tick before deadline")
	}
	// t=15: exactly deadline => expired (left-closed).
	if _, err := s.Expire(15); err != nil {
		t.Fatal(err)
	}
	if s.IsEvicting(id) {
		t.Fatal("at deadline the eviction must already be void")
	}
	if ready, _ := s.PodReady(id); !ready {
		t.Fatal("expiry must restore pre-admit readiness (ready=true)")
	}
	log.logf("admit@10 grace=5 deadline=15; Expire@14 still evicting; Expire@15 voided & ready restored; basis: left-closed deadline")
	st, _ := s.BudgetQuota(16, b.ID)
	if st.CurrentReady != 3 || st.DisruptionAllowed != 1 {
		t.Fatalf("restored readiness must return allowance, got %+v", st)
	}
	// Unready-at-admit restores to unready.
	relaxed := PodDisruptionBudget{ID: b.ID, Selector: b.Selector, MinAvailable: abs(1)}
	if err := s.UpsertBudget(17, relaxed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReady(18, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evict(19, id, 3); err != nil { // deadline 22
		t.Fatal(err)
	}
	got, err := s.Expire(22)
	if err != nil || len(got) != 1 {
		t.Fatalf("exact-deadline expiry expected one pod, got %v err=%v", got, err)
	}
	if ready, _ := s.PodReady(id); ready {
		t.Fatal("unready pod must restore to unready")
	}
}

func TestExpiryBeforeEveryDecision(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(1)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := s.UpsertPod(0, pod("ns", name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	// Consume the only allowance at t=0, grace 10.
	if _, err := s.Evict(0, PodID{"ns", "a"}, 10); err != nil {
		t.Fatal(err)
	}
	// At t=10 the first eviction is void, so the second is admitted even
	// without an explicit Expire call.
	if _, err := s.Evict(10, PodID{"ns", "b"}, 10); err != nil {
		t.Fatalf("due expiry must be applied before adjudication: %v", err)
	}
}

func TestConfirmAndCancel(t *testing.T) {
	s := NewService()
	if err := s.UpsertPod(0, pod("ns", "a", PhaseRunning, true, nil)); err != nil {
		t.Fatal(err)
	}
	id := PodID{"ns", "a"}
	if err := s.Confirm(1, id); err == nil {
		t.Fatal("confirm of non-evicting pod must be InvalidArgument")
	} else {
		mustKind(t, err, KindInvalidArgument)
	}
	if _, err := s.Evict(2, id, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(3, id); err != nil {
		t.Fatal(err)
	}
	if s.IsEvicting(id) {
		t.Fatal("cancel must clear evicting")
	}
	if _, err := s.Evict(4, id, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(5, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.PodReady(id); ok {
		t.Fatal("confirm removes the pod")
	}
}

func TestClockBacktrackRejectsWithoutStateChange(t *testing.T) {
	s := NewService()
	if err := s.UpsertPod(10, pod("ns", "a", PhaseRunning, true, nil)); err != nil {
		t.Fatal(err)
	}
	err := s.UpsertPod(9, pod("ns", "b", PhaseRunning, true, nil))
	mustKind(t, err, KindClockBacktrack)
	if _, err := s.Evict(10, PodID{"ns", "b"}, 1); err == nil {
		t.Fatal("clock-backtrack call must not have inserted pod b")
	}
}

// TestSelectorSubsetOfLabelsAndDelete is a regression for two index bugs:
// a budget whose selector requires only one of the pod's labels MUST match,
// and deleting such a pod (especially after lifecycle changes) must not leave
// a dangling membership or stale ready counter.
func TestSelectorSubsetOfLabelsAndDelete(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "b"}, MinAvailable: abs(0)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	p := pod("ns", "p2", PhaseRunning, true, map[string]string{"app": "b", "tier": "b"})
	if err := s.UpsertPod(0, p); err != nil {
		t.Fatal(err)
	}
	st, _ := s.BudgetQuota(0, b.ID)
	if st.Expected != 1 || st.CurrentReady != 1 {
		t.Fatalf("subset selector must match, got %+v", st)
	}
	if _, err := s.Evict(1, PodID{"ns", "p2"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePod(3, PodID{"ns", "p2"}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.BudgetQuota(4, b.ID)
	if st.Expected != 0 || st.CurrentReady != 0 {
		t.Fatalf("delete must remove membership/ready, got %+v", st)
	}
}
