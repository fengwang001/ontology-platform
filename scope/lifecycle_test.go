package scope

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 语义 5：已结束的父亲上派生，子立即结束且钩子仍恰好执行一次。
func TestChildOfEndedScope(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, time.Time{})
	root.Cancel(Reason("gone"))
	c := root.Child(time.Time{})
	mustClosed(t, c)
	if !errors.Is(c.Err(), ErrAncestorEnded) || c.Origin() != root || c.Reason() != Reason("gone") {
		t.Fatalf("child of ended: %v %q", c.Err(), c.Reason())
	}
	var n atomic.Int32
	c.OnDone(func() { n.Add(1) })
	if n.Load() != 1 {
		t.Fatalf("hook ran %d times, want 1", n.Load())
	}
}

// 语义 6：钩子 LIFO、恰好一次、panic 不中断。
func TestHooksLIFOAndPanicSafe(t *testing.T) {
	clk := newTestClock()
	s := NewRoot(clk.Now, time.Time{})
	var mu sync.Mutex
	var order []int
	record := func(i int) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, i)
		}
	}
	s.OnDone(record(1))
	s.OnDone(func() { panic("boom") })
	s.OnDone(record(3))
	s.Cancel(Reason("x"))
	if len(order) != 2 || order[0] != 3 || order[1] != 1 {
		t.Fatalf("hook order = %v, want [3 1]", order)
	}
	s.OnDone(record(4))
	if len(order) != 3 || order[2] != 4 {
		t.Fatalf("late hook order = %v", order)
	}
}

// 语义 7：结束传播到所有后代，Done 只关闭一次。
func TestPropagationToAllDescendants(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, time.Time{})
	a := root.Child(time.Time{})
	b := a.Child(time.Time{})
	c := b.Child(time.Time{})
	d := root.Child(time.Time{})
	root.Cancel(Reason("all"))
	for _, s := range []*Scope{root, a, b, c, d} {
		mustClosed(t, s)
	}
	if !errors.Is(c.Err(), ErrAncestorEnded) || c.Origin() != root {
		t.Fatalf("deep descendant: %v", c.Err())
	}
	root.Cancel(Reason("again"))
	root.Tick()
	mustClosed(t, root)
}

// 语义 8：时间只走注入时钟，左闭右开判定。
func TestTickUsesInjectedClock(t *testing.T) {
	clk := newTestClock()
	deadline := clk.Now().Add(time.Second)
	s := NewRoot(clk.Now, deadline)
	s.Tick()
	mustOpen(t, s)
	clk.Advance(time.Second - time.Nanosecond)
	s.Tick()
	mustOpen(t, s)
	clk.Advance(time.Nanosecond)
	s.Tick()
	mustClosed(t, s)
	if !errors.Is(s.Err(), ErrDeadlineExceeded) {
		t.Fatalf("err = %v", s.Err())
	}
}

// 语义 7（并发）：并发 Cancel/Tick 不重复关闭、不死锁，钩子恰好一次。
func TestConcurrentCancelAndTick(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, clk.Now().Add(time.Minute))
	var scopes []*Scope
	for i := 0; i < 4; i++ {
		child := root.Child(time.Time{})
		scopes = append(scopes, child, child.Child(time.Time{}))
	}
	scopes = append(scopes, root)
	var hooks atomic.Int32
	var wg sync.WaitGroup
	for _, s := range scopes {
		for j := 0; j < 3; j++ {
			s := s
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.OnDone(func() { hooks.Add(1) })
				s.Cancel(Reason("race"))
				s.Tick()
			}()
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		clk.Advance(2 * time.Minute)
		root.Tick()
	}()
	wg.Wait()
	root.Cancel(Reason("final"))
	if got, want := hooks.Load(), int32(len(scopes)*3); got != want {
		t.Fatalf("hooks ran %d times, want %d", got, want)
	}
	for _, s := range scopes {
		mustClosed(t, s)
	}
}
