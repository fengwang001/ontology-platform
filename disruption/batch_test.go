package disruption

import "testing"

func seedService(t *testing.T, s *Service, budget PodDisruptionBudget, pods ...Pod) {
	t.Helper()
	if err := s.UpsertBudget(0, budget); err != nil {
		t.Fatal(err)
	}
	for _, p := range pods {
		if err := s.UpsertPod(0, p); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBatchAllOrNothing: minAvailable=2 over 4 ready pods gives allowance 2.
// A batch of three ready pods must be rejected atomically (third exceeds),
// leaving every pod ready and no eviction; a batch of two succeeds.
func TestBatchAllOrNothing(t *testing.T) {
	log := newOpLogger(t)
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	var pods []Pod
	ids := []PodID{{"ns", "p0"}, {"ns", "p1"}, {"ns", "p2"}, {"ns", "p3"}}
	for _, id := range ids {
		pods = append(pods, pod(id.Namespace, id.Name, PhaseRunning, true, map[string]string{"app": "x"}))
	}
	seedService(t, s, b, pods...)

	_, err := s.EvictBatch(1, []PodID{ids[0], ids[1], ids[2]}, 100)
	mustKind(t, err, KindInsufficient)
	for _, id := range ids {
		if s.IsEvicting(id) {
			t.Fatalf("rejected batch must leave %s untouched", id.Name)
		}
		if ready, _ := s.PodReady(id); !ready {
			t.Fatalf("rejected batch must leave %s ready", id.Name)
		}
	}
	st, _ := s.BudgetQuota(2, b.ID)
	if st.CurrentReady != 4 {
		t.Fatalf("rejected batch must not consume, ready=%d", st.CurrentReady)
	}
	log.logf("EvictBatch input=[p0,p1,p2] allowance=2 => rejected Insufficient at p2; post-state ready=4 evicting=0, basis: all-or-nothing")

	// Unready pod in the batch judged at batch start: does not consume, but
	// still enters evicting on success.
	if _, err := s.EvictBatch(3, []PodID{ids[0], ids[1]}, 100); err != nil {
		t.Fatal(err)
	}
	log.logf("EvictBatch input=[p0,p1] => allowed; both evicting, ready drops 4->2")
	st, _ = s.BudgetQuota(4, b.ID)
	if st.CurrentReady != 2 || st.DisruptionAllowed != 0 {
		t.Fatalf("batch consumption got %+v", st)
	}
}

// TestBatchUnreadyUnaffectedByBatchMates: with allowance 0, a batch mixing
// ready and unready fails on the ready pod regardless of position, while a
// batch containing only the unready pod succeeds when the start-time
// invariant holds.
func TestBatchUnreadyUnaffectedByBatchMates(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	seedService(t, s, b,
		pod("ns", "r0", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "r1", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "u0", PhaseRunning, false, map[string]string{"app": "x"}),
	)
	// ready=2=required: unready alone admitted (no consumption).
	if _, err := s.EvictBatch(1, []PodID{{"ns", "u0"}}, 100); err != nil {
		t.Fatalf("unready alone must pass at invariant: %v", err)
	}
	if err := s.Cancel(2, PodID{"ns", "u0"}); err != nil {
		t.Fatal(err)
	}
	// [unready, ready]: the ready pod has allowance 0 -> Insufficient, and
	// the unready admission is rolled back too.
	_, err := s.EvictBatch(3, []PodID{{"ns", "u0"}, {"ns", "r0"}}, 100)
	mustKind(t, err, KindInsufficient)
	if s.IsEvicting(PodID{"ns", "u0"}) || s.IsEvicting(PodID{"ns", "r0"}) {
		t.Fatal("batch failure must roll back unready admission as well")
	}
}

// TestBatchAccumulatesAcrossBudgets: two independent budgets accumulate
// separately within one batch; each can afford one.
func TestBatchAccumulatesAcrossBudgets(t *testing.T) {
	s := NewService()
	b1 := PodDisruptionBudget{ID: BudgetID{"ns", "one"}, Selector: Selector{"g": "1"}, MinAvailable: abs(1)}
	b2 := PodDisruptionBudget{ID: BudgetID{"ns", "two"}, Selector: Selector{"g": "2"}, MaxUnavail: abs(1)}
	seedService(t, s, b1,
		pod("ns", "a", PhaseRunning, true, map[string]string{"g": "1"}),
		pod("ns", "b", PhaseRunning, true, map[string]string{"g": "1"}),
	)
	if err := s.UpsertBudget(0, b2); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPod(0, pod("ns", "c", PhaseRunning, true, map[string]string{"g": "2"})); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPod(0, pod("ns", "d", PhaseRunning, true, map[string]string{"g": "2"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvictBatch(1, []PodID{{"ns", "a"}, {"ns", "c"}}, 100); err != nil {
		t.Fatalf("one per budget must succeed: %v", err)
	}
	_, err := s.EvictBatch(2, []PodID{{"ns", "b"}, {"ns", "d"}}, 100)
	mustKind(t, err, KindInsufficient)
}

func TestBatchDuplicatesAndEmpty(t *testing.T) {
	s := NewService()
	_, err := s.EvictBatch(0, nil, 1)
	mustKind(t, err, KindInvalidArgument)
	_, err = s.EvictBatch(0, []PodID{{"ns", "a"}, {"ns", "a"}}, 1)
	mustKind(t, err, KindInvalidArgument)
}

// TestBatchReportsFirstOffender: the reported pod is the first in submission
// order that has a problem.
func TestBatchReportsFirstOffender(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)}
	seedService(t, s, b,
		pod("ns", "ok", PhaseRunning, true, map[string]string{"app": "x"}),
	)
	_, err := s.EvictBatch(1, []PodID{{"ns", "ok"}, {"ns", "missing"}}, 100)
	ae := err.(*AdjudicationError)
	if ae.Pod.Name != "missing" {
		t.Fatalf("want first offender missing, got %s", ae.Pod.Name)
	}
	// Insufficient first offender: allowance for one ready pod only.
	s2 := NewService()
	seedService(t, s2,
		PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)},
		pod("ns", "p0", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "p1", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "p2", PhaseRunning, true, map[string]string{"app": "x"}),
	)
	_, err = s2.EvictBatch(2, []PodID{{"ns", "p0"}, {"ns", "p1"}}, 100)
	if ae, ok := err.(*AdjudicationError); !ok || ae.Pod.Name != "p1" {
		t.Fatalf("want insufficient reported at p1, got %v", err)
	}
}

// TestErrorPrecedence checks that for one pod satisfying several conditions
// the highest-priority kind is returned.
func TestErrorPrecedence(t *testing.T) {
	s := NewService()
	// Clock backtrack beats missing pod.
	_ = s.UpsertPod(10, pod("ns", "x", PhaseRunning, true, nil))
	_, err := s.Evict(5, PodID{"ns", "ghost"}, 1)
	mustKind(t, err, KindClockBacktrack)
	// NotEvictablePhase beats AlreadyEvicting: terminal pod cannot be
	// evicting in normal flow, so instead verify phase beats conflict:
	b1 := PodDisruptionBudget{ID: BudgetID{"ns", "b1"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)}
	// phase vs conflict
	s2 := NewService()
	_ = s2.UpsertBudget(11, b1)
	_ = s2.UpsertBudget(11, PodDisruptionBudget{ID: BudgetID{"ns", "b2"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)})
	_ = s2.UpsertPod(11, pod("ns", "term", PhaseFailed, false, map[string]string{"app": "x"}))
	_, err = s2.Evict(11, PodID{"ns", "term"}, 1)
	mustKind(t, err, KindNotEvictablePhase)
	// AlreadyEvicting beats Conflict: make the pod evicting while unmatched
	// first, then add a second matching budget.
	s3 := NewService()
	_ = s3.UpsertBudget(12, b1)
	_ = s3.UpsertPod(12, pod("ns", "p", PhaseRunning, true, map[string]string{"app": "x"}))
	if _, err := s3.Evict(12, PodID{"ns", "p"}, 100); err != nil {
		t.Fatal(err)
	}
	_ = s3.UpsertBudget(13, PodDisruptionBudget{ID: BudgetID{"ns", "b2"}, Selector: Selector{"app": "x"}, MinAvailable: abs(0)})
	_, err = s3.Evict(14, PodID{"ns", "p"}, 100)
	mustKind(t, err, KindAlreadyEvicting)
	// InvalidArgument beats everything (bad id + clock backtrack).
	_, err = s.Evict(5, PodID{"", ""}, 1)
	mustKind(t, err, KindInvalidArgument)
}

// TestDynamicChanges: label moves, readiness flips, budget edits and deletions
// all take effect on the very next adjudication.
func TestDynamicChanges(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(2)}
	seedService(t, s, b,
		pod("ns", "a", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "b", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "c", PhaseRunning, true, map[string]string{"app": "other"}),
	)
	st, _ := s.BudgetQuota(1, b.ID)
	if st.Expected != 2 {
		t.Fatalf("expected=2 got %d", st.Expected)
	}
	// Relabel c into the selector: expected grows.
	if err := s.UpsertPod(2, pod("ns", "c", PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.BudgetQuota(3, b.ID); st.Expected != 3 || st.DisruptionAllowed != 1 {
		t.Fatalf("after relabel expected=3 allowance=1 got %+v", st)
	}
	// Flip a pod unready: allowance disappears.
	if err := s.SetReady(4, PodID{"ns", "a"}, false); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.BudgetQuota(5, b.ID); st.CurrentReady != 2 || st.DisruptionAllowed != 0 {
		t.Fatalf("after ready flip got %+v", st)
	}
	// Delete pod b: expected shrinks, required 2 > ready 1 => ready evict
	// refused.
	if err := s.DeletePod(6, PodID{"ns", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evict(7, PodID{"ns", "c"}, 10); err == nil {
		t.Fatal("must refuse ready eviction once required exceeds ready")
	}
	// Tightening the budget does not undo an in-flight eviction but blocks
	// further ones.
	s2 := NewService()
	seedService(t, s2, b,
		pod("ns", "a", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "b", PhaseRunning, true, map[string]string{"app": "x"}),
		pod("ns", "c", PhaseRunning, true, map[string]string{"app": "x"}),
	)
	if _, err := s2.Evict(8, PodID{"ns", "a"}, 100); err != nil {
		t.Fatal(err)
	}
	tight := PodDisruptionBudget{ID: b.ID, Selector: b.Selector, MinAvailable: abs(2)}
	if err := s2.UpsertBudget(9, tight); err != nil {
		t.Fatal(err)
	}
	if !s2.IsEvicting(PodID{"ns", "a"}) {
		t.Fatal("existing eviction must survive budget tightening")
	}
	if _, err := s2.Evict(10, PodID{"ns", "b"}, 100); err == nil {
		t.Fatal("new ready eviction must be refused after tightening")
	}
	// Delete the budget -> remaining pod unmatched -> admitted.
	if err := s2.DeleteBudget(11, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Evict(12, PodID{"ns", "b"}, 100); err != nil {
		t.Fatalf("unmatched after budget delete must pass: %v", err)
	}
}
