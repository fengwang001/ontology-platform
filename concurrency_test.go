package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentUpdatesUniqueVersions hammers Update from many goroutines
// and asserts every successful update gets a distinct, never-reused version.
func TestConcurrentUpdatesUniqueVersions(t *testing.T) {
	m := newTestManager(t)
	const workers = 16
	const perWorker = 50
	results := make([][]int64, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				v, err := m.Update(map[string]any{"retries": i % 11})
				if err != nil {
					t.Errorf("update: %v", err)
					return
				}
				results[w] = append(results[w], v)
			}
		}(w)
	}
	close(start)
	wg.Wait()
	seen := make(map[int64]bool)
	total := 0
	for _, vs := range results {
		for _, v := range vs {
			if seen[v] {
				t.Fatalf("duplicate version number %d", v)
			}
			seen[v] = true
			total++
		}
	}
	if total != workers*perWorker {
		t.Fatalf("got %d versions, want %d", total, workers*perWorker)
	}
	if m.CurrentVersion() != int64(1+total) {
		t.Fatalf("current = %d, want %d", m.CurrentVersion(), 1+total)
	}
}

// TestConcurrentAcquireRelease exercises Acquire/Release/Get/GC together
// and asserts the leak count returns to exactly zero.
func TestConcurrentAcquireRelease(t *testing.T) {
	m := newTestManager(t)
	const workers = 16
	const perWorker = 100
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				snap := m.Acquire()
				if _, err := snap.Get("host"); err != nil {
					t.Errorf("get: %v", err)
				}
				if i%3 == 0 {
					if _, err := m.Update(map[string]any{"host": "x"}); err != nil {
						t.Errorf("update: %v", err)
					}
				}
				if i%5 == 0 {
					m.GC()
				}
				snap.Release()
				snap.Release() // idempotent even under concurrency
			}
		}(w)
	}
	close(start)
	wg.Wait()
	if rep := m.Leaks(); rep.Total != 0 {
		t.Fatalf("leaks after all releases = %+v, want 0", rep)
	}
	// With no outstanding references, every old version must be collectable.
	reclaimed := m.GC()
	if len(reclaimed) == 0 {
		t.Fatal("expected GC to reclaim old versions")
	}
	cur := m.CurrentVersion()
	for _, v := range reclaimed {
		if v >= cur {
			t.Fatalf("GC reclaimed current/future version %d (current %d)", v, cur)
		}
	}
	if rep := m.Leaks(); rep.Total != 0 {
		t.Fatalf("leaks after GC = %+v, want 0", rep)
	}
}

// TestConcurrentSwitchAndUpdate ensures version numbers stay unique and
// monotonic when SwitchTo races with Update.
func TestConcurrentSwitchAndUpdate(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	var wg sync.WaitGroup
	start := make(chan struct{})
	versions := make(chan int64, 200)
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 10; i++ {
				if w%2 == 0 {
					v, err := m.SwitchTo(1)
					if err != nil {
						t.Errorf("switch: %v", err)
						return
					}
					versions <- v
				} else {
					v, err := m.Update(map[string]any{"retries": i})
					if err != nil {
						t.Errorf("update: %v", err)
						return
					}
					versions <- v
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(versions)
	seen := make(map[int64]bool)
	max := int64(0)
	for v := range versions {
		if seen[v] {
			t.Fatalf("duplicate version %d across switch/update", v)
		}
		seen[v] = true
		if v > max {
			max = v
		}
	}
	if m.CurrentVersion() != max {
		t.Fatalf("current = %d, want max issued %d", m.CurrentVersion(), max)
	}
}
