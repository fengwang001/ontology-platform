package scheduler

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAddCancelAdvance hammers the scheduler from many
// goroutines: workers Add and Cancel while another goroutine Advances.
// Afterwards: SelfCheck is clean, no cancelled timer ever fired, and
// fired + cancelled == added. No sleeps are used for timing.
func TestConcurrentAddCancelAdvance(t *testing.T) {
	var (
		mu        sync.Mutex
		fired     = make(map[uint64]bool)
		cancelled = make(map[uint64]bool)
	)
	var added atomic.Int64
	s, err := New(0, Config{OnFire: func(ev Event) {
		mu.Lock()
		fired[ev.ID] = true
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var advWG sync.WaitGroup
	advWG.Add(1)
	go func() {
		defer advWG.Done()
		for !stop.Load() {
			if _, err := s.Advance(1); err != nil {
				t.Errorf("Advance: %v", err)
				return
			}
		}
	}()
	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id, err := s.Add(int64(i%50)+1, nil)
				if err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				added.Add(1)
				if i%2 == 0 {
					if err := s.Cancel(id); err == nil {
						mu.Lock()
						cancelled[id] = true
						mu.Unlock()
					}
				}
			}
		}(w)
	}
	wg.Wait()
	stop.Store(true)
	advWG.Wait()
	if _, err := s.Advance(100); err != nil { // flush everything remaining
		t.Fatal(err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for id := range cancelled {
		if fired[id] {
			t.Fatalf("cancelled timer %d fired", id)
		}
	}
	total := int64(len(fired) + len(cancelled))
	if total != added.Load() {
		t.Fatalf("fired(%d) + cancelled(%d) = %d, added = %d",
			len(fired), len(cancelled), total, added.Load())
	}
}
