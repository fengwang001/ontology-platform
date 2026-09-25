package idle

import (
	"fmt"
	"testing"

	"ontology/wtm"
)

// TestLateCheckConstant is white-box: it reads the unexported counter.
// Deciding lateness compares ts-delay with the single current watermark
// and never rescans history, so the examined-event count must stay the
// same small constant (0) regardless of how many events were fed.
func TestLateCheckConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(3, 5)
		for i := 0; i < m; i++ {
			s.Feed(fmt.Sprintf("k%d", i), int64(100+i), int64(i))
		}
		before := s.lateChecks
		s.Feed("ontime", int64(m)+1000, int64(m)) // accepted, above wm
		afterOn := s.lateChecks
		s.Feed("late", 0, int64(m)+1) // strictly late, dropped
		afterLate := s.lateChecks

		if before != 0 || afterOn != 0 || afterLate != 0 {
			t.Fatalf("m=%d: late checks depend on history: %d,%d,%d",
				m, before, afterOn, afterLate)
		}
		if s.Dropped() != 1 {
			t.Fatalf("m=%d: dropped=%d want 1", m, s.Dropped())
		}
	}
}

// TestInitialNegativeInfinity pins the lastEventPT=-inf rule: before
// any Feed the state is negative infinity and a first Tick already
// counts as idle (without any pt-(-inf) overflow).
func TestInitialNegativeInfinity(t *testing.T) {
	s := New(3, 5)
	if s.lastPT != wtm.NegInf || s.wm != wtm.NegInf {
		t.Fatal("initial wm and lastEventPT must be negative infinity")
	}
	s.Tick(10)
	if s.Watermark() != 7 {
		t.Fatalf("tick before first feed: wm=%d want 7", s.Watermark())
	}
}
