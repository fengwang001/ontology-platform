package drain

import (
	"sync"
	"testing"
)

// Regression: the release closure decremented inFlight unconditionally,
// so duplicate calls (even concurrent ones) drove InFlight negative.
func TestReleaseExactlyOnceRegression(t *testing.T) {
	g := New(nil)
	release, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}

	release()
	release()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release()
		}()
	}
	wg.Wait()

	s := g.Stats()
	if s.InFlight != 0 {
		t.Fatalf("InFlight: got %d, want 0", s.InFlight)
	}
	if s.Admitted != 1 {
		t.Fatalf("Admitted: got %d, want 1", s.Admitted)
	}
}
