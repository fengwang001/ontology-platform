package drain

import (
	"errors"
	"testing"
	"time"
)

// Regression: Enter returned ErrShuttingDown without incrementing the
// rejected counter, so rejections were invisible to Stats.
func TestRegressionRejectionCounted(t *testing.T) {
	g := New(nil)
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(time.Now().Add(time.Hour)) }()
	waitDraining(t, g)

	before := g.Stats().Rejected
	if _, err := g.Enter(); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Enter: got %v, want ErrShuttingDown", err)
	}
	if got := g.Stats().Rejected; got != before+1 {
		t.Fatalf("Rejected: got %d, want %d", got, before+1)
	}
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// Regression: the release closure decremented inFlight on every call,
// so a duplicate release drove the counter negative.
func TestRegressionReleaseExactlyOnce(t *testing.T) {
	g := New(nil)
	release, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	release()
	release()
	release()
	if got := g.Stats().InFlight; got != 0 {
		t.Fatalf("InFlight after duplicate releases: got %d, want 0", got)
	}
}

// Regression: Shutdown zeroed inFlight when the deadline passed, so
// Stats lost the true in-flight count and late releases went negative.
func TestRegressionTimeoutPreservesInFlight(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.Now)
	r1, _ := g.Enter()
	r2, _ := g.Enter()

	done := make(chan error, 1)
	go func() { done <- g.Shutdown(clock.Now().Add(time.Second)) }()
	waitDraining(t, g)
	clock.Advance(time.Minute)
	g.Tick()
	if err := <-done; !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("Shutdown: got %v, want ErrDrainTimeout", err)
	}
	if got := g.Stats().InFlight; got != 2 {
		t.Fatalf("InFlight after timeout: got %d, want 2", got)
	}

	r1()
	r2()
	if got := g.Stats().InFlight; got != 0 {
		t.Fatalf("InFlight after late releases: got %d, want 0", got)
	}
}
