package scope

import (
	"errors"
	"testing"
	"time"
)

// 回归：Child 只在零值时继承父 deadline，声明更晚时未收紧成父的。
// 根因：Child 缺少 min(父, 自己声明的) 收紧分支。
func TestRegressionChildDeadlineTightensToParent(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, clk.Now().Add(10*time.Second))
	child := root.Child(clk.Now().Add(time.Hour))
	if !child.Deadline().Equal(root.Deadline()) {
		t.Fatalf("declared-later deadline not tightened: got %v, want %v",
			child.Deadline(), root.Deadline())
	}
	clk.Advance(11 * time.Second)
	child.Tick()
	if !errors.Is(child.Err(), ErrDeadlineExceeded) {
		t.Fatalf("child should time out at parent's deadline: %v", child.Err())
	}
}

// 回归：深层后代的 Origin 变成了直接父亲而非最早触发者。
// 根因：afterEnd 传播时把 s（直接父亲）当作 origin 传给子节点。
func TestRegressionOriginIsEarliestTrigger(t *testing.T) {
	clk := newTestClock()
	root := NewRoot(clk.Now, time.Time{})
	mid := root.Child(time.Time{})
	leaf := mid.Child(time.Time{})
	root.Cancel(Reason("root gone"))
	if leaf.Origin() != root {
		t.Fatalf("leaf origin = %p, want root %p", leaf.Origin(), root)
	}
	if leaf.Reason() != Reason("root gone") {
		t.Fatalf("leaf reason = %q, want %q", leaf.Reason(), "root gone")
	}
	if !errors.Is(leaf.Err(), ErrAncestorEnded) {
		t.Fatalf("leaf err = %v", leaf.Err())
	}
}

// 回归：now 恰好等于 deadline 时 Tick 不判超时。
// 根因：Tick 用了 now.After(deadline)（严格大于），违反左闭右开约定。
func TestRegressionTickAtExactDeadline(t *testing.T) {
	clk := newTestClock()
	deadline := clk.Now().Add(time.Second)
	s := NewRoot(clk.Now, deadline)
	clk.Advance(time.Second)
	if !clk.Now().Equal(deadline) {
		t.Fatal("clock should be exactly at deadline")
	}
	s.Tick()
	mustClosed(t, s)
	if !errors.Is(s.Err(), ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", s.Err())
	}
}
