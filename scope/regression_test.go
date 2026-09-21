package scope

import (
	"errors"
	"testing"
	"time"
)

// 回归：子作用域声明比父更晚的 deadline 必须被收紧成父的。
// 根因：Child 只对零值做了继承，漏掉了与父 deadline 取 min 的收紧分支。
func TestRegressionChildDeadlineClampedToParent(t *testing.T) {
	clk := newTestClock()
	parent := NewRoot(clk.Now, clk.Now().Add(10*time.Second))
	child := parent.Child(clk.Now().Add(time.Hour))
	if got := child.Deadline(); !got.Equal(parent.Deadline()) {
		t.Fatalf("child deadline = %v, want clamped to %v", got, parent.Deadline())
	}
}

// 回归：隔代连坐时 Origin 必须是最早触发者（祖父），而非直接父亲。
// 根因：afterEnd 传播时把直接父亲 s 当作 origin，没有透传原始触发者。
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
		t.Fatalf("leaf reason = %q", leaf.Reason())
	}
}

// 回归：now 恰好等于 deadline 时必须判超时（左闭右开）。
// 根因：Tick 用 now.After(deadline) 判定，把等于 deadline 的时刻漏判为未超时。
func TestRegressionTickFiresExactlyAtDeadline(t *testing.T) {
	clk := newTestClock()
	deadline := clk.Now()
	s := NewRoot(clk.Now, deadline)
	s.Tick()
	mustClosed(t, s)
	if !errors.Is(s.Err(), ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", s.Err())
	}
}
