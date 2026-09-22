package scheduler

import (
	"errors"
	"testing"

	"ontology/timer"
)

func TestHandleIdempotency(t *testing.T) {
	s := newSched(t, Config{})
	mustAdd := func(d int64) *timer.Timer {
		tm, err := s.Add(d, nil)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	twice := mustAdd(10)
	if err := s.Cancel(twice); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(twice); !errors.Is(err, ErrAlreadyCancelled) {
		t.Fatalf("second Cancel = %v, want ErrAlreadyCancelled", err)
	}
	fired := mustAdd(1)
	if err := s.Advance(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(fired); !errors.Is(err, ErrAlreadyFired) {
		t.Fatalf("Cancel fired = %v, want ErrAlreadyFired", err)
	}
	if err := s.Reset(twice, 5); !errors.Is(err, ErrAlreadyCancelled) {
		t.Fatalf("Reset cancelled = %v, want ErrAlreadyCancelled", err)
	}
	if err := s.Reset(fired, 5); !errors.Is(err, ErrAlreadyFired) {
		t.Fatalf("Reset fired = %v, want ErrAlreadyFired", err)
	}
	pending := mustAdd(10)
	if err := s.Reset(pending, 5); err != nil {
		t.Fatalf("Reset pending = %v, want nil", err)
	}
	if err := s.Cancel(nil); !errors.Is(err, ErrUnknownTimer) {
		t.Fatalf("Cancel nil = %v, want ErrUnknownTimer", err)
	}
	if err := s.Cancel(timer.New(0, 0, nil)); !errors.Is(err, ErrUnknownTimer) {
		t.Fatalf("Cancel foreign = %v, want ErrUnknownTimer", err)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestResetFromNow(t *testing.T) {
	s := newSched(t, Config{})
	rec := &recorder{}
	tm, err := s.Add(10, rec.add(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(5); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(tm, 10); err != nil { // 新到期时刻 = 5 + 10 = 15
		t.Fatal(err)
	}
	if err := s.Advance(9); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.get()); n != 0 {
		t.Fatalf("fired at tick 14, want pending until 15")
	}
	if err := s.Advance(1); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.get()); n != 1 {
		t.Fatalf("fired %d times at tick 15, want 1", n)
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
}
