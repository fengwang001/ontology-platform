package drain

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentConservation hammers the gate from many goroutines and
// verifies the counter invariants hold exactly at the end.
func TestConcurrentConservation(t *testing.T) {
	g := New(nil)

	var attempts, admitted, released atomic.Int64
	var negativeSeen atomic.Bool

	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if g.Stats().InFlight < 0 {
					negativeSeen.Store(true)
				}
			}
		}
	}()

	const workers = 32
	const rounds = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				release, err := g.Enter()
				attempts.Add(1)
				if err != nil {
					continue
				}
				admitted.Add(1)
				release()
				released.Add(1)
			}
		}()
	}

	time.AfterFunc(5*time.Millisecond, func() {
		_ = g.Shutdown(time.Now().Add(10 * time.Second))
	})
	wg.Wait()
	close(stop)
	sampler.Wait()

	if err := g.Shutdown(time.Now()); err != nil {
		t.Fatalf("Shutdown after all releases: got %v, want nil", err)
	}

	s := g.Stats()
	if negativeSeen.Load() {
		t.Fatal("InFlight went negative during the run")
	}
	if int64(s.Admitted) != admitted.Load() {
		t.Fatalf("Admitted: got %d, want %d", s.Admitted, admitted.Load())
	}
	if int64(s.Admitted+s.Rejected) != attempts.Load() {
		t.Fatalf("Admitted+Rejected: got %d, want %d", s.Admitted+s.Rejected, attempts.Load())
	}
	if int64(s.InFlight) != admitted.Load()-released.Load() {
		t.Fatalf("InFlight: got %d, want %d", s.InFlight, admitted.Load()-released.Load())
	}
	if s.InFlight != 0 {
		t.Fatalf("InFlight at rest: got %d, want 0", s.InFlight)
	}
	if !s.Done {
		t.Fatal("Done must be true after Shutdown completes")
	}
}

// TestConcurrentDuplicateRelease calls one release from many
// goroutines; exactly one decrement must happen.
func TestConcurrentDuplicateRelease(t *testing.T) {
	g := New(nil)
	release, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release()
		}()
	}
	wg.Wait()

	if got := g.Stats().InFlight; got != 0 {
		t.Fatalf("InFlight: got %d, want 0", got)
	}
}
