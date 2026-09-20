package drain

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReleaseIdempotent(t *testing.T) {
	g := New(nil)
	release, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	release()
	release()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release()
		}()
	}
	wg.Wait()

	s := g.Stats()
	if s.InFlight != 0 {
		t.Fatalf("InFlight: got %d, want 0 (must never go negative)", s.InFlight)
	}
	if s.Admitted != 1 {
		t.Fatalf("Admitted: got %d, want 1", s.Admitted)
	}
}

func TestShutdownIdempotent(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.Now)
	hold, _ := g.Enter()

	const callers = 5
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			results <- g.Shutdown(clock.Now().Add(time.Duration(i+1) * time.Minute))
		}(i)
	}
	waitDraining(t, g)
	admittedAfterProbe := g.Stats().Admitted
	clock.Advance(time.Hour)
	g.Tick()

	for i := 0; i < callers; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrDrainTimeout) {
				t.Fatalf("caller %d: got %v, want ErrDrainTimeout", i, err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent Shutdown caller did not return")
		}
	}
	if err := g.Shutdown(clock.Now()); !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("repeat Shutdown after completion: got %v", err)
	}

	s := g.Stats()
	if s.Admitted != admittedAfterProbe {
		t.Fatalf("Admitted changed by repeated Shutdown: got %d, want %d", s.Admitted, admittedAfterProbe)
	}
	if s.Rejected == 0 {
		t.Fatal("expected rejections from waitDraining probe")
	}
	hold()
	if got := g.Stats().InFlight; got != 0 {
		t.Fatalf("InFlight: got %d, want 0", got)
	}
}
