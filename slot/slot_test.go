package slot

import (
	"testing"
	"time"
)

func TestAllocateBumpsEpochAndGuardsDoubleWait(t *testing.T) {
	s := New()
	deadline := time.Unix(10, 0)
	epoch, ok := s.Allocate(deadline)
	if !ok || epoch != 1 {
		t.Fatalf("first allocate = %d,%v, want 1,true", epoch, ok)
	}
	if s.State() != Waiting || s.Deadline() != deadline {
		t.Fatalf("waiting state not set: %v %v", s.State(), s.Deadline())
	}
	if _, ok := s.Allocate(deadline.Add(time.Second)); ok {
		t.Fatal("reallocate while waiting must fail")
	}
}

func TestExactlyOneCompletion(t *testing.T) {
	s := New()
	epoch, _ := s.Allocate(time.Unix(10, 0))
	if !s.Complete(epoch) {
		t.Fatal("first complete must succeed")
	}
	if s.Complete(epoch) {
		t.Fatal("second complete must fail")
	}
	if s.Cancel(epoch) {
		t.Fatal("cancel after complete must fail")
	}
}

func TestStaleEpochRejected(t *testing.T) {
	s := New()
	old, _ := s.Allocate(time.Unix(10, 0))
	s.Cancel(old)
	s.Release()
	newEpoch, _ := s.Allocate(time.Unix(20, 0))
	if s.Complete(old) {
		t.Fatal("old epoch must not complete current generation")
	}
	if !s.Complete(newEpoch) {
		t.Fatal("current epoch must complete")
	}
}

func TestExpirationIsLeftClosed(t *testing.T) {
	s := New()
	_, _ = s.Allocate(time.Unix(10, 0))
	if s.Expired(time.Unix(9, 0)) {
		t.Fatal("one instant before deadline must not expire")
	}
	if !s.Expired(time.Unix(10, 0)) {
		t.Fatal("now == deadline must expire")
	}
	if !s.Expire() || s.State() != TimedOut {
		t.Fatal("expire did not move slot to TimedOut")
	}
}

func TestReleaseCycle(t *testing.T) {
	s := New()
	epoch, _ := s.Allocate(time.Unix(10, 0))
	s.Complete(epoch)
	if !s.Release() || s.State() != Vacant {
		t.Fatal("finished slot must be releasable")
	}
	if s.Epoch() != 1 {
		t.Fatal("released slot must remember its last epoch")
	}
}
