package drain

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// waitDraining spins until the gate starts rejecting, proving that
// Shutdown has begun. Any extra admissions are released immediately.
func waitDraining(t *testing.T, g *Gate) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		release, err := g.Enter()
		if errors.Is(err, ErrShuttingDown) {
			return
		}
		if release != nil {
			release()
		}
		if time.Now().After(deadline) {
			t.Fatal("gate never started draining")
		}
	}
}

func TestEnterRejectedAfterShutdown(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.Now)

	hold, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter before shutdown: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(clock.Now().Add(time.Hour)) }()
	waitDraining(t, g)

	before := g.Stats()
	release, err := g.Enter()
	if !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Enter after shutdown: got %v, want ErrShuttingDown", err)
	}
	if release != nil {
		t.Fatal("rejected Enter must return a nil release")
	}
	after := g.Stats()
	if after.Rejected != before.Rejected+1 {
		t.Fatalf("Rejected: got %d, want %d", after.Rejected, before.Rejected+1)
	}
	if after.Admitted != before.Admitted {
		t.Fatalf("Admitted changed on rejection: %d -> %d", before.Admitted, after.Admitted)
	}

	hold()
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestShutdownWaitsForInFlight(t *testing.T) {
	g := New(nil)
	releases := make([]func(), 3)
	for i := range releases {
		r, err := g.Enter()
		if err != nil {
			t.Fatalf("Enter: %v", err)
		}
		releases[i] = r
	}

	done := make(chan error, 1)
	go func() { done <- g.Shutdown(time.Now().Add(time.Hour)) }()
	waitDraining(t, g)

	releases[0]()
	releases[1]()
	select {
	case <-done:
		t.Fatal("Shutdown returned while a request was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if got := g.Stats().InFlight; got != 1 {
		t.Fatalf("InFlight during drain: got %d, want 1", got)
	}

	releases[2]()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: got %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return promptly after last release")
	}
	if s := g.Stats(); !s.Done || s.InFlight != 0 {
		t.Fatalf("Stats after drain: %+v", s)
	}
}

func TestDrainTimeoutKeepsInFlight(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.Now)

	r1, _ := g.Enter()
	r2, _ := g.Enter()
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(clock.Now().Add(10 * time.Second)) }()
	waitDraining(t, g)

	clock.Advance(11 * time.Second)
	g.Tick()

	select {
	case err := <-done:
		if !errors.Is(err, ErrDrainTimeout) {
			t.Fatalf("Shutdown: got %v, want ErrDrainTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not observe the passed deadline after Tick")
	}
	s := g.Stats()
	if s.InFlight != 2 {
		t.Fatalf("InFlight after timeout: got %d, want 2 (must not be cleared)", s.InFlight)
	}
	if !s.Done {
		t.Fatal("Done must be true after the shutdown procedure ends")
	}

	// Late releases must still be accounted for.
	r1()
	if got := g.Stats().InFlight; got != 1 {
		t.Fatalf("InFlight after late release: got %d, want 1", got)
	}
	r2()
	s = g.Stats()
	if s.InFlight != 0 {
		t.Fatalf("InFlight after all late releases: got %d, want 0", s.InFlight)
	}
	if !s.Done {
		t.Fatal("Done must stay true")
	}
}
