package disruption

import (
	"fmt"
	"testing"
)

// TestCostIndependentOfClusterSize proves (not measures) that single eviction
// and quota queries touch only state relevant to the target. The proof uses
// explicit primitive counters: if the hot path scanned unrelated pods or
// budgets the counters would grow with the filler size; instead they must be
// byte-identical at every scale.
func TestCostIndependentOfClusterSize(t *testing.T) {
	fillSizes := []int{0, 100, 1_000, 10_000}
	var prev *Probe
	var prevQuota *Probe
	for _, fill := range fillSizes {
		s := buildCluster(fill)

		// Single eviction on a pod in the target namespace.
		s.ResetProbe()
		dec, err := s.Evict(1, PodID{"tgt", "victim"}, 1000)
		if err != nil || !dec.Allowed {
			t.Fatalf("eviction failed at fill=%d: %v", fill, err)
		}
		got := s.ProbeSnapshot()

		// Quota query on the target budget (on a fresh service so expiry heap
		// state does not vary).
		s2 := buildCluster(fill)
		s2.ResetProbe()
		if _, err := s2.BudgetQuota(1, BudgetID{"tgt", "tb"}); err != nil {
			t.Fatal(err)
		}
		gotQuota := s2.ProbeSnapshot()

		t.Logf("complexity input fillerPods/Budgets=%d => Evict probes {pod:%d budget:%d member:%d expiry:%d}; Quota probes {budget:%d expiry:%d}",
			fill, got.PodProbe, got.BudgetProbe, got.MemberProbe, got.ExpiryProbe,
			gotQuota.BudgetProbe, gotQuota.ExpiryProbe)

		if prev != nil {
			if got != *prev {
				t.Fatalf("eviction probes grew with cluster size: %+v vs %+v", got, *prev)
			}
			if gotQuota != *prevQuota {
				t.Fatalf("quota probes grew with cluster size: %+v vs %+v", gotQuota, *prevQuota)
			}
		}
		prev, prevQuota = &got, &gotQuota
	}

	// Structural assertion on the actual constants: the eviction touches
	// exactly one pod record, exactly one (matching) budget, never the filler.
	s := buildCluster(10_000)
	s.ResetProbe()
	if _, err := s.Evict(1, PodID{"tgt", "victim"}, 1000); err != nil {
		t.Fatal(err)
	}
	pr := s.ProbeSnapshot()
	if pr.PodProbe != 1 {
		t.Fatalf("eviction must touch exactly 1 pod record, got %d", pr.PodProbe)
	}
	if pr.BudgetProbe != 1 {
		t.Fatalf("eviction must touch exactly the 1 matching budget, got %d", pr.BudgetProbe)
	}
	if pr.MemberProbe != 1 {
		t.Fatalf("one matched budget's membership must be adjusted once, got %d", pr.MemberProbe)
	}

	// Unrelated budgets in the SAME namespace with disjoint selector keys
	// must not be probed either.
	s2 := buildCluster(0)
	for i := 0; i < 5000; i++ {
		b := PodDisruptionBudget{
			ID:           BudgetID{"tgt", "noise" + itoa(i)},
			Selector:     Selector{fmt.Sprintf("noise-key-%d", i): "v"},
			MinAvailable: abs(0),
		}
		if err := s2.UpsertBudget(0, b); err != nil {
			t.Fatal(err)
		}
	}
	s2.ResetProbe()
	if _, err := s2.Evict(1, PodID{"tgt", "victim"}, 1000); err != nil {
		t.Fatal(err)
	}
	pr2 := s2.ProbeSnapshot()
	if pr2.BudgetProbe != 1 {
		t.Fatalf("same-namespace disjoint-selector budgets must not be probed, got %d", pr2.BudgetProbe)
	}

	// Same selector key, different value: not on the pod's posting list, so
	// never probed either.
	s3 := buildCluster(0)
	for i := 0; i < 5000; i++ {
		b := PodDisruptionBudget{
			ID:           BudgetID{"tgt", "samekey" + itoa(i)},
			Selector:     Selector{"app": fmt.Sprintf("other-%d", i)},
			MinAvailable: abs(0),
		}
		if err := s3.UpsertBudget(0, b); err != nil {
			t.Fatal(err)
		}
	}
	s3.ResetProbe()
	if _, err := s3.Evict(1, PodID{"tgt", "victim"}, 1000); err != nil {
		t.Fatal(err)
	}
	pr3 := s3.ProbeSnapshot()
	if pr3.BudgetProbe != 1 {
		t.Fatalf("same-key different-value budgets must not be probed, got %d", pr3.BudgetProbe)
	}
}

// buildCluster creates one target budget + one target pod plus `fill`
// irrelevant namespaces, each with one budget and one pod. With fill=0 only
// the target pair exists.
func buildCluster(fill int) *Service {
	s := NewService()
	tb := PodDisruptionBudget{ID: BudgetID{"tgt", "tb"}, Selector: Selector{"app": "api"}, MinAvailable: abs(0)}
	if err := s.UpsertBudget(0, tb); err != nil {
		panic(err)
	}
	if err := s.UpsertPod(0, pod("tgt", "victim", PhaseRunning, true, map[string]string{"app": "api"})); err != nil {
		panic(err)
	}
	for i := 0; i < fill; i++ {
		ns := "fill-" + itoa(i)
		b := PodDisruptionBudget{ID: BudgetID{ns, "fb"}, Selector: Selector{"app": "fill"}, MinAvailable: abs(0)}
		if err := s.UpsertBudget(0, b); err != nil {
			panic(err)
		}
		if err := s.UpsertPod(0, pod(ns, "fp", PhaseRunning, true, map[string]string{"app": "fill"})); err != nil {
			panic(err)
		}
	}
	return s
}
