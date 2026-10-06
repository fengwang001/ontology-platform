package disruption

import (
	"sync"
	"testing"
)

// TestConcurrentNoOverConsumption: 200 goroutines race to evict ready pods
// covered by one budget whose allowance is exactly K. Exactly K evictions must
// be admitted (serialization under the mutex), never more, even with -race.
func TestConcurrentNoOverConsumption(t *testing.T) {
	const total = 200
	const allowed = 7
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(total - allowed)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		name := "p" + itoa(i)
		if err := s.UpsertPod(0, pod("ns", name, PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	admits := 0
	var reasons []Kind
	start := make(chan struct{})
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Evict(1, PodID{"ns", "p" + itoa(i)}, 1000)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				admits++
			} else if k, ok := KindOf(err); ok {
				reasons = append(reasons, k)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if admits != allowed {
		t.Fatalf("exactly %d evictions must be admitted under concurrency, got %d", allowed, admits)
	}
	for _, k := range reasons {
		if k != KindInsufficient {
			t.Fatalf("only Insufficient rejections expected, got %s", k)
		}
	}
	st, _ := s.BudgetQuota(2, b.ID)
	if st.CurrentReady != total-allowed {
		t.Fatalf("ready after race = %d, want %d", st.CurrentReady, total-allowed)
	}
	t.Logf("concurrency input=%d goroutines, budget allowance=%d => admitted=%d rejected=%d, basis: mutex-serialized adjudication",
		total, allowed, admits, len(reasons))
}

// TestConcurrentBatchAtomic: a batch consuming an entire budget races with
// single evictions; the aggregate admission must never exceed the allowance,
// and the batch either fully commits or does not commit at all.
func TestConcurrentBatchAtomic(t *testing.T) {
	const total = 50
	const allowance = 10
	s := NewService()
	b := PodDisruptionBudget{ID: BudgetID{"ns", "b"}, Selector: Selector{"app": "x"}, MinAvailable: abs(total - allowance)}
	if err := s.UpsertBudget(0, b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if err := s.UpsertPod(0, pod("ns", "p"+itoa(i), PhaseRunning, true, map[string]string{"app": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	var batchIDs []PodID
	for i := 0; i < allowance; i++ {
		batchIDs = append(batchIDs, PodID{"ns", "p" + itoa(i)})
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	batchOK := false
	singleAdmits := 0
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if _, err := s.EvictBatch(1, batchIDs, 1000); err == nil {
			mu.Lock()
			batchOK = true
			mu.Unlock()
		}
	}()
	for i := allowance; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := s.Evict(1, PodID{"ns", "p" + itoa(i)}, 1000); err == nil {
				mu.Lock()
				singleAdmits++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	evicting := 0
	for i := 0; i < total; i++ {
		if s.IsEvicting(PodID{"ns", "p" + itoa(i)}) {
			evicting++
		}
	}
	if evicting > allowance {
		t.Fatalf("batch/single race over-consumed: evicting=%d allowance=%d", evicting, allowance)
	}
	if batchOK {
		for _, id := range batchIDs {
			if !s.IsEvicting(id) {
				t.Fatal("committed batch must be indivisible: every member evicting")
			}
		}
	} else {
		for _, id := range batchIDs {
			if s.IsEvicting(id) {
				t.Fatal("rejected batch must not partially commit")
			}
		}
	}
	t.Logf("atomicity input=batch(%d)+%d racing singles => evicting=%d batchCommitted=%v singles=%d, basis: single mutex + commit-after-validate",
		len(batchIDs), total-allowance, evicting, batchOK, singleAdmits)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
