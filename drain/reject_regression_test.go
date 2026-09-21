package drain

import (
	"errors"
	"testing"
	"time"
)

// Regression: Enter's draining branch returned ErrShuttingDown without
// incrementing the rejected counter, so Rejected stayed 0 forever.
func TestRejectedCountedOnShutdownRegression(t *testing.T) {
	g := New(nil)
	hold, err := g.Enter()
	if err != nil {
		t.Fatalf("Enter: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(time.Now().Add(time.Hour)) }()
	waitDraining(t, g)
	base := g.Stats()

	const attempts = 3
	for i := 0; i < attempts; i++ {
		release, err := g.Enter()
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("Enter during shutdown: got %v, want ErrShuttingDown", err)
		}
		if release != nil {
			t.Fatal("rejected Enter must return a nil release")
		}
	}

	s := g.Stats()
	if s.Rejected != base.Rejected+attempts {
		t.Fatalf("Rejected: got %d, want %d", s.Rejected, base.Rejected+attempts)
	}
	if s.Admitted != base.Admitted {
		t.Fatalf("Admitted changed by rejections: %d -> %d", base.Admitted, s.Admitted)
	}

	hold()
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
