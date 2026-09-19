package scope

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mustClosed(t *testing.T, s *Scope) {
	t.Helper()
	select {
	case <-s.Done():
	default:
		t.Fatal("Done channel should be closed")
	}
}

func mustOpen(t *testing.T, s *Scope) {
	t.Helper()
	select {
	case <-s.Done():
		t.Fatal("Done channel should be open")
	default:
	}
}

// 语义 1：截止时间只能收紧，零值继承父。
func TestDeadlineOnlyTightens(t *testing.T) {
	clk := newTestClock()
	rootDeadline := clk.Now().Add(10 * time.Second)
	root := NewRoot(clk.Now, rootDeadline)

	later := root.Child(rootDeadline.Add(5 * time.Second))
	if got := later.Deadline(); !got.Equal(rootDeadline) {
		t.Fatalf("later child deadline = %v, want %v", got, rootDeadline)
	}
	inherited := root.Child(time.Time{})
	if got := inherited.Deadline(); !got.Equal(rootDeadline) {
		t.Fatalf("zero child deadline = %v, want inherited %v", got, rootDeadline)
	}
	earlier := root.Child(clk.Now().Add(3 * time.Second))
	if got := earlier.Deadline(); !got.Equal(clk.Now().Add(3 * time.Second)) {
		t.Fatalf("earlier child deadline = %v", got)
	}
	grand := earlier.Child(clk.Now().Add(8 * time.Second))
	if got := grand.Deadline(); !got.Equal(earlier.Deadline()) {
		t.Fatalf("grandchild deadline = %v, want tightened %v", got, earlier.Deadline())
	}
}

// 语义 2：三类原因可用 errors.Is 区分。
func TestThreeErrorKinds(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, clk.Now().Add(time.Second))
	canceled := root.Child(time.Time{})
	canceled.Cancel(Reason("manual"))
	if !errors.Is(canceled.Err(), ErrCanceled) || errors.Is(canceled.Err(), ErrDeadlineExceeded) {
		t.Fatalf("canceled err = %v", canceled.Err())
	}
	timedOut := root.Child(clk.Now().Add(time.Second))
	clk.Advance(2 * time.Second)
	timedOut.Tick()
	if !errors.Is(timedOut.Err(), ErrDeadlineExceeded) || errors.Is(timedOut.Err(), ErrCanceled) {
		t.Fatalf("timed out err = %v", timedOut.Err())
	}
	ended := NewRoot(clk.Now, time.Time{})
	ended.Cancel(Reason("bye"))
	victim := ended.Child(time.Time{})
	if !errors.Is(victim.Err(), ErrAncestorEnded) || errors.Is(victim.Err(), ErrCanceled) {
		t.Fatalf("ancestor-ended err = %v", victim.Err())
	}
}

// 语义 3：连坐归因到最早触发者，原因文字原样透出。
func TestOriginAttribution(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, time.Time{})
	a := root.Child(time.Time{})
	b := a.Child(time.Time{})
	c := b.Child(time.Time{})
	root.Cancel(Reason("root shutdown"))
	for _, s := range []*Scope{a, b, c} {
		if s.Origin() != root {
			t.Fatalf("origin = %v, want root", s.Origin())
		}
		if s.Reason() != Reason("root shutdown") {
			t.Fatalf("reason = %q", s.Reason())
		}
	}
	mid := NewRoot(clk.Now, time.Time{})
	x := mid.Child(time.Time{})
	y := x.Child(time.Time{})
	x.Cancel(Reason("x canceled"))
	if y.Origin() != x || y.Reason() != Reason("x canceled") {
		t.Fatalf("mid-level origin/reason wrong: %v %q", y.Origin(), y.Reason())
	}
}

// 语义 4：首个原因不可改写。
func TestFirstCauseWins(t *testing.T) {
	clk := newTestClock()
	s := NewRoot(clk.Now, clk.Now().Add(time.Hour))
	s.Cancel(Reason("first"))
	s.Cancel(Reason("second"))
	clk.Advance(2 * time.Hour)
	s.Tick()
	if s.Reason() != Reason("first") || !errors.Is(s.Err(), ErrCanceled) || s.Origin() != s {
		t.Fatalf("first cause overwritten: %v %q", s.Err(), s.Reason())
	}
	t2 := NewRoot(clk.Now, clk.Now())
	t2.Tick()
	t2.Cancel(Reason("late"))
	if !errors.Is(t2.Err(), ErrDeadlineExceeded) || t2.Reason() != Reason("deadline exceeded") {
		t.Fatalf("timeout overwritten: %v %q", t2.Err(), t2.Reason())
	}
}
