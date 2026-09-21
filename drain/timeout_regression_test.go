package drain

import (
	"errors"
	"testing"
	"time"
)

// Regression: the timeout branch of Shutdown zeroed inFlight, so Stats
// lost the true outstanding count and late releases drove it negative.
func TestTimeoutKeepsAndCountsInFlightRegression(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.Now)

	r1, _ := g.Enter()
	r2, _ := g.Enter()
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(clock.Now().Add(5 * time.Second)) }()
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
