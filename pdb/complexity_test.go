package pdb

import (
	"fmt"
	"testing"
)

// TestComplexityIndependentOfUnrelatedObjects is a structural (not timing)
// proof that single eviction and budget query work is independent of the
// number of unrelated pods and budgets.
//
// Setup: one "hot" namespace with the pod/budget under test. Every noise
// namespace contains many pods and budgets whose selector keys are disjoint
// from the hot labels, so they are genuinely unrelated. Additionally the hot
// namespace contains many pods with disjoint label keys (unrelated pods that
// nevertheless share the namespace).
//
// We read the instrumented probe counters (BudgetScans = candidate budgets
// touched by an eviction; PodScans = pods touched by a budget upsert/query).
// If work were linear in total cluster size, these counters would grow as
// noise grows; the assertions require them to stay constant.
func TestComplexityIndependentOfUnrelatedObjects(t *testing.T) {
	lg := newOpLogger(t)

	measure := func(noise int) (evictBudgetProbes, queryPodProbes int64) {
		s := New(60 * 1e9)
		s.EnableInstrumentation()
		clock := 0
		now := func() (sec int) {
			clock++
			return clock
		}
		// Hot budget.
		must(t, s.UpsertBudget(BudgetSpec{Name: "hot", Namespace: "hot",
			Selector: Selector{"app": "shop"}, MinAvailable: minAv(0)}, at(now())), "hot budget")
		// Unrelated pods in the hot namespace, different label keys.
		for i := 0; i < noise; i++ {
			must(t, s.UpsertPod(mkPod("hot", fmt.Sprintf("noise-%d", i), true, PhaseRunning,
				map[string]string{fmt.Sprintf("k%d", i): "v"}), at(now())), "noise pod hot ns")
		}
		// Unrelated namespaces: each has many pods and many budgets.
		for n := 0; n < noise; n++ {
			ns := fmt.Sprintf("noise-ns-%d", n)
			for j := 0; j < 5; j++ {
				must(t, s.UpsertPod(mkPod(ns, fmt.Sprintf("p%d", j), true, PhaseRunning,
					map[string]string{fmt.Sprintf("nk%d", n): "x"}), at(now())), "noise pod")
				must(t, s.UpsertBudget(BudgetSpec{Name: fmt.Sprintf("nb-%d", j), Namespace: ns,
					Selector: Selector{fmt.Sprintf("nk%d", n): "x"}, MinAvailable: minAv(0)},
					at(now())), "noise budget")
			}
		}
		must(t, s.UpsertPod(mkPod("hot", "target", true, PhaseRunning,
			map[string]string{"app": "shop"}), at(now())), "target pod")

		// Reset counters after setup; measure the steady-state operations.
		s.ReadCounters()

		must(t, s.Evict(PodRef{"hot", "target"}, at(now())), "evict target")
		c := s.ReadCounters()
		evictBudgetProbes = c.BudgetScans

		must(t, s.Cancel(PodRef{"hot", "target"}, at(now())), "cancel")
		// Budget query membership enumeration cost.
		st, err := s.Budget("hot", "hot", at(now()))
		must(t, err, "query")
		c2 := s.ReadCounters()
		queryPodProbes = c2.PodScans
		if st.Expected != 1 {
			t.Fatalf("hot budget expected=1 got %d", st.Expected)
		}
		return evictBudgetProbes, queryPodProbes
	}

	base1, baseQ1 := measure(1)
	lg.log("noise=1:     eviction touched %d candidate budgets; budget query touched %d pods",
		base1, baseQ1)
	base100, baseQ100 := measure(100)
	lg.log("noise=100:   eviction touched %d candidate budgets; budget query touched %d pods",
		base100, baseQ100)
	base500, baseQ500 := measure(500)
	lg.log("noise=500:   eviction touched %d candidate budgets; budget query touched %d pods",
		base500, baseQ500)

	if base1 != base100 || base100 != base500 {
		t.Fatalf("eviction work grew with unrelated objects: %d,%d,%d", base1, base100, base500)
	}
	if baseQ1 != baseQ100 || baseQ100 != baseQ500 {
		t.Fatalf("budget query work grew with unrelated objects: %d,%d,%d", baseQ1, baseQ100, baseQ500)
	}
	if base1 == 0 || baseQ1 == 0 {
		t.Fatal("probe counters must observe the related work")
	}
	lg.log("PROOF: probe counts constant across 1/100/500 noise objects => " +
		"single eviction and budget query are independent of unrelated pods/budgets")
}
