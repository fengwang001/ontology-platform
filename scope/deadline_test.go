package scope

import (
	"errors"
	"testing"
	"time"
)

// 语义 1：截止时间只能收紧；零值表示继承父的。
func TestDeadlineOnlyTightens(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()

	root := NewRoot(clk.now, base.Add(10*time.Second))

	later := root.Child(base.Add(20 * time.Second))
	if got := later.Deadline(); !got.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("child declared later deadline not tightened: got %v, want %v", got, base.Add(10*time.Second))
	}

	earlier := root.Child(base.Add(5 * time.Second))
	if got := earlier.Deadline(); !got.Equal(base.Add(5 * time.Second)) {
		t.Fatalf("child declared earlier deadline altered: got %v", got)
	}

	inherited := root.Child(time.Time{})
	if got := inherited.Deadline(); !got.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("zero deadline must inherit parent's: got %v", got)
	}

	// 多级收紧：5s 的儿子声明 8s 的孙子，应被收紧到 5s。
	grand := earlier.Child(base.Add(8 * time.Second))
	if got := grand.Deadline(); !got.Equal(base.Add(5 * time.Second)) {
		t.Fatalf("nested deadline not tightened through chain: got %v", got)
	}
}

// 语义 2 + 语义 8：左闭右开的截止判定与自身超时错误。
func TestDeadlineExceeded(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(10*time.Second))

	clk.advance(9 * time.Second)
	root.Tick()
	assertOpen(t, root) // now 严格早于 deadline，未结束

	clk.advance(time.Second)
	root.Tick()
	assertEnded(t, root)
	assertErrIs(t, root, ErrDeadlineExceeded)
	if root.Origin() != root {
		t.Fatalf("self-timeout Origin must be itself")
	}
	if root.Reason() != deadlineReason {
		t.Fatalf("self-timeout Reason = %q", root.Reason())
	}
}

// 语义 2：显式 Cancel 得到 ErrCanceled。
func TestExplicitCancel(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})

	root.Cancel("no longer needed")
	assertEnded(t, root)
	assertErrIs(t, root, ErrCanceled)
	if root.Origin() != root {
		t.Fatalf("self-cancel Origin must be itself")
	}
	if root.Reason() != "no longer needed" {
		t.Fatalf("Reason = %q, want %q", root.Reason(), "no longer needed")
	}
}

// 三种错误必须能彼此用 errors.Is 区分。
func TestErrorSentinelsDistinct(t *testing.T) {
	sentinels := []error{ErrDeadlineExceeded, ErrCanceled, ErrAncestorEnded}
	for i, err := range sentinels {
		for j, target := range sentinels {
			got := errors.Is(err, target)
			if (i == j) != got {
				t.Fatalf("errors.Is(%v, %v) = %v", err, target, got)
			}
		}
	}
	if errors.Is(nil, ErrCanceled) {
		t.Fatal("nil error must not match any sentinel")
	}
}

func TestNoErrBeforeEnd(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})
	if root.Err() != nil || root.Reason() != "" || root.Origin() != nil {
		t.Fatal("open scope must have nil Err/Reason/Origin")
	}
	root.Tick()
	assertOpen(t, root)
	if root.Err() != nil {
		t.Fatal("sentinel errors must be distinguishable")
	}
}
