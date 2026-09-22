package slot

import (
	"errors"
	"testing"
	"time"
)

func TestIDEncodeRoundTrip(t *testing.T) {
	id := Encode(7, 42)
	if got := id.Index(); got != 7 {
		t.Fatalf("index = %d, want 7", got)
	}
	if got := id.Generation(); got != 42 {
		t.Fatalf("generation = %d, want 42", got)
	}
}

func TestGenerationDistinguishesReuse(t *testing.T) {
	s := New(3)
	first := s.Begin(time.Unix(0, 0).Add(10 * time.Second))
	if err := s.TimeOut(); err != nil {
		t.Fatal(err)
	}
	second := s.Begin(time.Unix(0, 0).Add(20 * time.Second))
	if first == second {
		t.Fatal("reused slot must produce a different ID")
	}
	if second.Generation() != first.Generation()+1 {
		t.Fatal("generation must increment on reuse")
	}
}

func TestExactlyOneSettlement(t *testing.T) {
	s := New(0)
	s.Begin(time.Unix(10, 0))
	if err := s.Complete(); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{s.Complete, s.TimeOut, s.Cancel} {
		if !errors.Is(fn(), ErrInvalidTransition) {
			t.Fatal("second settlement must fail")
		}
	}
	if s.State() != Completed {
		t.Fatalf("state = %v, want completed", s.State())
	}
}

func TestExpirationIsLeftClosedRightOpen(t *testing.T) {
	deadline := time.Unix(10, 0)
	s := New(0)
	s.Begin(deadline)
	if s.ExpiredAt(deadline.Add(-1)) {
		t.Fatal("must still be pending one instant before deadline")
	}
	if !s.ExpiredAt(deadline) {
		t.Fatal("now == deadline must count as expired")
	}
}

func TestRemaining(t *testing.T) {
	now := time.Unix(0, 0)
	s := New(0)
	s.Begin(now.Add(10 * time.Second))
	if got := s.Remaining(now.Add(3 * time.Second)); got != 7*time.Second {
		t.Fatalf("remaining = %v, want 7s", got)
	}
	if err := s.Cancel(); err != nil {
		t.Fatal(err)
	}
	if got := s.Remaining(now); got != 0 {
		t.Fatalf("settled remaining = %v, want 0", got)
	}
}
