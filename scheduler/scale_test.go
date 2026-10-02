package scheduler

import (
	"sync"
	"testing"
)

// avgExamined runs steps with n simultaneously ready tasks (all crossing
// aging tiers at the same ticks) and returns the average number of
// candidates examined per Step.
func avgExamined(t *testing.T, n, steps int, th int) float64 {
	t.Helper()
	s, err := NewScheduler(2, 10, 1, n)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if err := s.Add(i, 1, th, 1_000_000); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < steps; i++ {
		s.Step()
	}
	return float64(s.examined) / float64(steps)
}

// The decision must not scan the ready set: the examined-per-Step ratio
// between 100 and 10000 ready tasks must stay far below the ~100x a linear
// scan would show.
func TestExaminedScalesSublinearly(t *testing.T) {
	for _, th := range []int{255, 1} { // shielded runner / preemptive regime
		small := avgExamined(t, 100, 300, th)
		large := avgExamined(t, 10000, 300, th)
		ratio := large / small
		t.Logf("th=%d: avg examined per Step: n=100 -> %.3f, n=10000 -> %.3f, ratio %.3f",
			th, small, large, ratio)
		if ratio >= 4 {
			t.Fatalf("th=%d: examined ratio %.2f >= 4 (linear scan would be ~100)", th, ratio)
		}
	}
}

// Concurrent Add/Step/Now/Count must be race-free and leave the books
// balanced: every executed tick is accounted for by work done or remaining.
func TestConcurrentUse(t *testing.T) {
	s, err := NewScheduler(3, 2, 2, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	work := map[int]int{}
	executed := map[int]int{}
	finished := map[int]int{}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := g*1000 + i + 1
				w := 1 + (i % 5)
				if err := s.Add(id, i%6, i%6, w); err == nil {
					mu.Lock()
					work[id] = w
					mu.Unlock()
				}
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				run, done := s.Step()
				mu.Lock()
				if run != 0 {
					executed[run]++
				}
				if done != 0 {
					finished[done] = executed[done]
					delete(executed, done)
				}
				mu.Unlock()
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				_ = s.Now()
				_ = s.Count()
			}
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for id, ex := range finished {
		if ex != work[id] {
			t.Fatalf("task %d finished after %d ticks, work=%d", id, ex, work[id])
		}
		delete(work, id)
	}
	for id, ex := range executed {
		rt := s.tasks[id]
		if rt == nil {
			t.Fatalf("task %d executed but not registered", id)
		}
		if rt.work-rt.rem != ex {
			t.Fatalf("task %d work-rem=%d, executed=%d", id, rt.work-rt.rem, ex)
		}
		delete(work, id)
	}
	for id, w := range work {
		rt := s.tasks[id]
		if rt == nil {
			t.Fatalf("task %d neither finished nor registered", id)
		}
		if rt.rem != w {
			t.Fatalf("task %d never ran but rem=%d, work=%d", id, rt.rem, w)
		}
	}
	if s.running != nil && s.tasks[s.running.id] != s.running {
		t.Fatal("running task not registered")
	}
}
