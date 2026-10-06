package disruption

import "testing"

func TestPendingEvictable(t *testing.T) {
	s := NewService()
	if err := s.UpsertPod(0, pod("ns", "pend", PhasePending, false, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evict(1, PodID{"ns", "pend"}, 5); err != nil {
		t.Fatalf("pending unmatched pod must be evictable: %v", err)
	}
}

func TestDeleteEvictingKeepsBudgetConsistent(t *testing.T) {
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(1)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPod(0, pod("ns", "a", PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPod(0, pod("ns", "b2", PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evict(1, PodID{"ns", "a"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePod(2, PodID{"ns", "a"}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.BudgetQuota(3, b.ID)
	if st.Expected != 1 || st.CurrentReady != 1 || st.DisruptionAllowed != 0 {
		t.Fatalf("unexpected status after deleting evicting pod: %+v", st)
	}
}
