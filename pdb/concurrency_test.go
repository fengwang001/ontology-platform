package pdb

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentNoOverspend hammers many ready pods against a budget that
// permits exactly K disruptions. Precisely K evictions must be allowed, no
// more, and the invariant ready >= required must hold at every instant.
func TestConcurrentNoOverspend(t *testing.T) {
	lg := newOpLogger(t)
	const total, allowedK = 200, 17
	s := New(60 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(total - allowedK)}, at(0)), "budget")
	for i := 0; i < total; i++ {
		must(t, s.UpsertPod(mkPod("ns", fmt.Sprintf("p%03d", i), true, PhaseRunning,
			map[string]string{"app": "x"}), at(1)), "pod")
	}

	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			err := s.Evict(PodRef{"ns", fmt.Sprintf("p%03d", i)}, at(2))
			if err == nil {
				atomic.AddInt64(&granted, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	st, err := s.Budget("ns", "b", at(3))
	must(t, err, "query")
	lg.log("concurrent: %d pods, allowance %d => granted=%d, post ready=%d required=%d",
		total, allowedK, atomic.LoadInt64(&granted), st.CurrentReady, st.RequiredReady)
	if atomic.LoadInt64(&granted) != allowedK {
		t.Fatalf("granted=%d want exactly %d (no overspend)", granted, allowedK)
	}
	if st.CurrentReady != total-allowedK {
		t.Fatalf("ready=%d want %d", st.CurrentReady, total-allowedK)
	}
}

// TestConcurrentBatchAtomicity races one batch (which consumes all remaining
// allowance) against single evictions: the batch is indivisible, so either the
// whole batch wins and singles get nothing, or the singles win first.
func TestConcurrentBatchAtomicity(t *testing.T) {
	lg := newOpLogger(t)
	const total = 100
	s := New(60 * 1e9)
	must(t, s.UpsertBudget(BudgetSpec{Name: "b", Namespace: "ns",
		Selector: Selector{"app": "x"}, MinAvailable: minAv(0)}, at(0)), "minAv=0")
	for i := 0; i < total; i++ {
		must(t, s.UpsertPod(mkPod("ns", fmt.Sprintf("p%03d", i), true, PhaseRunning,
			map[string]string{"app": "x"}), at(1)), "pod")
	}

	batchRefs := make([]PodRef, 0, total/2)
	for i := 0; i < total/2; i++ {
		batchRefs = append(batchRefs, PodRef{"ns", fmt.Sprintf("p%03d", i)})
	}

	var batchOK int64
	var singleGranted int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if s.EvictBatch(batchRefs, at(2)) == nil {
			atomic.StoreInt64(&batchOK, 1)
		}
	}()
	for i := total / 2; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if s.Evict(PodRef{"ns", fmt.Sprintf("p%03d", i)}, at(2)) == nil {
				atomic.AddInt64(&singleGranted, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	st, _ := s.Budget("ns", "b", at(3))
	lg.log("batch size=%d won=%d singles granted=%d post ready=%d (sum=%d)",
		len(batchRefs), atomic.LoadInt64(&batchOK), atomic.LoadInt64(&singleGranted),
		st.CurrentReady, int64(st.CurrentReady)+atomic.LoadInt64(&batchOK)*int64(len(batchRefs))+atomic.LoadInt64(&singleGranted))
	if st.CurrentReady < 0 {
		t.Fatal("ready counter went negative")
	}
	gotEvicting := int64(total) - int64(st.CurrentReady)
	want := atomic.LoadInt64(&batchOK)*int64(len(batchRefs)) + atomic.LoadInt64(&singleGranted)
	if gotEvicting != want {
		t.Fatalf("evicting=%d but granted sum=%d (batch was not atomic)", gotEvicting, want)
	}
	if gotEvicting > total {
		t.Fatal("overspend across batch and singles")
	}
}
