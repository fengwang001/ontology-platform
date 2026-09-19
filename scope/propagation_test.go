package scope

import (
	"errors"
	"testing"
	"time"
)

// 语义 3 + 7：取消沿整棵树传播；连坐后代归因为最早触发者，
// 透出其原始原因文字，错误为 ErrAncestorEnded。
func TestCancelPropagatesToAllDescendants(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})
	mid := root.Child(time.Time{})
	leafA := mid.Child(time.Time{})
	leafB := mid.Child(time.Time{})
	deep := leafA.Child(time.Time{})

	root.Cancel("root reason")

	for name, s := range map[string]*Scope{
		"mid": mid, "leafA": leafA, "leafB": leafB, "deep": deep,
	} {
		assertEnded(t, s)
		if s.Origin() != root {
			t.Fatalf("%s Origin = %p, want root %p", name, s.Origin(), root)
		}
		if s.Reason() != "root reason" {
			t.Fatalf("%s Reason = %q, want %q", name, s.Reason(), "root reason")
		}
		if !errors.Is(s.Err(), ErrAncestorEnded) {
			t.Fatalf("%s Err = %v, want ErrAncestorEnded", name, s.Err())
		}
	}

	if !errors.Is(root.Err(), ErrCanceled) {
		t.Fatalf("root Err = %v", root.Err())
	}
}

// 超时同样沿树传播，Origin 指向真正超时的祖先。
func TestDeadlinePropagatesThroughTree(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(10*time.Second))
	mid := root.Child(time.Time{})
	leaf := mid.Child(time.Time{})

	clk.advance(10 * time.Second)
	root.Tick()

	assertEnded(t, mid)
	assertEnded(t, leaf)
	if mid.Origin() != root || leaf.Origin() != root {
		t.Fatal("descendants must attribute timeout to root")
	}
	if !errors.Is(leaf.Err(), ErrAncestorEnded) {
		t.Fatalf("leaf Err = %v", leaf.Err())
	}
}

// 后代自身更早的截止时间可被 Tick 触发，Origin 为该后代自身。
func TestTighterChildDeadlineFiresFirst(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(10*time.Second))
	child := root.Child(base.Add(3 * time.Second))

	clk.advance(3 * time.Second)
	root.Tick()

	assertErrIs(t, child, ErrDeadlineExceeded)
	if child.Origin() != child {
		t.Fatal("child Origin must be itself when its own deadline fires")
	}
	assertOpen(t, root)
}

// 语义 5：在已结束的作用域上 Child()，新子立即连坐结束，
// Origin 指向原始触发者，收尾钩子仍恰好执行一次。
func TestChildOnEndedParent(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})
	mid := root.Child(time.Time{})
	root.Cancel("boom")

	var calls int
	late := mid.Child(time.Time{})
	assertEnded(t, late)
	assertErrIs(t, late, ErrAncestorEnded)
	if late.Origin() != root {
		t.Fatal("late child Origin must point at root")
	}
	if late.Reason() != "boom" {
		t.Fatalf("late child Reason = %q", late.Reason())
	}

	late.OnDone(func() { calls++ })
	late.OnDone(func() { calls++ })
	if calls != 2 {
		t.Fatalf("hooks on ended scope must run immediately once each, got %d", calls)
	}
}

// Tick 后时间继续推进不会改写状态（见语义 4 的配套场景）。
func TestTickOnEndedIsNoop(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(10*time.Second))
	root.Cancel("first")
	clk.advance(time.Hour)
	root.Tick()
	assertErrIs(t, root, ErrCanceled)
	if root.Reason() != "first" || root.Origin() != root {
		t.Fatal("later timeout must not overwrite cancellation")
	}
}
