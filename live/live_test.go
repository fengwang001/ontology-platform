package live

import (
	"errors"
	"testing"
)

func TestStreamEmitAndErrors(t *testing.T) {
	s := NewStream()
	e1, err := s.Emit(1000, Normal)
	if err != nil || e1.Seq != 1 || e1.Now != 1000 {
		t.Fatalf("e1 = %+v, %v", e1, err)
	}
	e2, err := s.Emit(1000, Hidden)
	if err != nil || e2.Seq != 2 {
		t.Fatalf("same now should be allowed, got %+v, %v", e2, err)
	}
	if _, err := s.Emit(999, Normal); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback err = %v", err)
	}
	if s.MaxNow() != 1000 {
		t.Fatalf("rejected emit moved clock: maxNow=%d", s.MaxNow())
	}
	if _, err := s.Emit(2000, Kind(99)); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("kind err = %v", err)
	}
	e3, err := s.Emit(3000, End)
	if err != nil || e3.Seq != 3 {
		t.Fatalf("end = %+v, %v", e3, err)
	}
	if !s.Ended() {
		t.Fatal("should be ended")
	}
	tE, ok := s.EndTime()
	if !ok || tE != 3000 {
		t.Fatalf("end time = %d, %v", tE, ok)
	}
	if _, err := s.Emit(3000, Normal); !errors.Is(err, ErrAlreadyEnded) {
		t.Fatalf("after end err = %v", err)
	}
	if got, ok := s.At(2); !ok || got.Kind != Hidden {
		t.Fatalf("at(2) = %+v, %v", got, ok)
	}
	if _, ok := s.At(4); ok {
		t.Fatal("at(4) should not exist")
	}
}
