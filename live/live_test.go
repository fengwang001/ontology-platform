package live

import (
	"errors"
	"testing"
)

func TestEmitSequence(t *testing.T) {
	s := NewStream()
	kinds := []Kind{Normal, Hidden, Normal, Hidden, End}
	for i, k := range kinds {
		ev, err := s.Emit(int64(1000*(i+1)), k)
		if err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
		if ev.Seq != int64(i+1) || ev.T != int64(1000*(i+1)) || ev.Kind != k {
			t.Fatalf("Emit %d = %+v", i, ev)
		}
	}
	if !s.Ended() || s.TEnd() != 5000 || s.Len() != 5 {
		t.Fatalf("ended=%v tEnd=%d len=%d", s.Ended(), s.TEnd(), s.Len())
	}
	if _, err := s.Emit(6000, Normal); !errors.Is(err, ErrEnded) {
		t.Fatalf("Emit after End = %v, want ErrEnded", err)
	}
	if _, err := s.Emit(6000, Hidden); !errors.Is(err, ErrEnded) {
		t.Fatalf("Emit Hidden after End = %v, want ErrEnded", err)
	}
}

func TestEmitInvalidKind(t *testing.T) {
	s := NewStream()
	if _, err := s.Emit(0, Kind(99)); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("Emit invalid kind = %v, want ErrInvalidKind", err)
	}
	if s.Len() != 0 {
		t.Fatalf("Len = %d after rejected emit", s.Len())
	}
}

func TestUpperBound(t *testing.T) {
	s := NewStream()
	for _, tm := range []int64{1000, 2000, 2000, 5000} {
		if _, err := s.Emit(tm, Normal); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		cutoff int64
		want   int64
	}{
		{-1, 0}, {0, 0}, {999, 0}, {1000, 1}, {1999, 1},
		{2000, 3}, {2001, 3}, {4999, 3}, {5000, 4}, {1 << 40, 4},
	}
	for _, c := range cases {
		if got := s.UpperBound(c.cutoff); got != c.want {
			t.Errorf("UpperBound(%d) = %d, want %d", c.cutoff, got, c.want)
		}
	}
}

func TestHiddenCount(t *testing.T) {
	s := NewStream()
	for _, k := range []Kind{Normal, Hidden, Normal, Hidden, End} {
		if _, err := s.Emit(1000, k); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ lo, hi, want int64 }{
		{0, 5, 2}, {0, 1, 0}, {1, 2, 1}, {2, 5, 1}, {0, 0, 0}, {5, 5, 0}, {3, 4, 1},
	}
	for _, c := range cases {
		if got := s.HiddenCount(c.lo, c.hi); got != c.want {
			t.Errorf("HiddenCount(%d,%d) = %d, want %d", c.lo, c.hi, got, c.want)
		}
	}
}

func TestTouchedCountsOnlyRecordReads(t *testing.T) {
	s := NewStream()
	for i := 0; i < 100; i++ {
		if _, err := s.Emit(int64(i), Hidden); err != nil {
			t.Fatal(err)
		}
	}
	s.ResetTouched()
	s.UpperBound(50)
	s.HiddenCount(0, 50)
	s.Len()
	s.Ended()
	s.TEnd()
	if got := s.Touched(); got != 0 {
		t.Fatalf("index reads touched %d records, want 0", got)
	}
	s.Event(1)
	s.Event(2)
	s.Event(2)
	if got := s.Touched(); got != 3 {
		t.Fatalf("Touched = %d, want 3", got)
	}
}
