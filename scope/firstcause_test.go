package scope

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// 语义 4：重复 Cancel 不改写首个原因。
func TestRepeatedCancelKeepsFirst(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})
	root.Cancel("first")
	root.Cancel("second")

	assertErrIs(t, root, ErrCanceled)
	if root.Reason() != "first" || root.Origin() != root {
		t.Fatalf("first cause overwritten: reason=%q origin-self=%v", root.Reason(), root.Origin() == root)
	}
}

// 语义 4：先 Cancel，随后 Tick 走到超时，仍保持 Cancel 的判定。
func TestCancelWinsOverLaterTimeout(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(time.Second))
	root.Cancel("manual")
	clk.advance(time.Minute)
	root.Tick()

	assertErrIs(t, root, ErrCanceled)
	if root.Reason() != "manual" {
		t.Fatalf("reason = %q", root.Reason())
	}
}

// 语义 4：先超时，再 Cancel，保持超时判定。
func TestTimeoutWinsOverLaterCancel(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(time.Second))
	clk.advance(time.Second)
	root.Tick()
	root.Cancel("too late")

	assertErrIs(t, root, ErrDeadlineExceeded)
	if root.Reason() != deadlineReason {
		t.Fatalf("reason = %q", root.Reason())
	}
}

// 语义 7：并发 Cancel/Tick/派生/注册钩子不得重复关闭 channel、死锁或丢失传播。
func TestConcurrentEndings(t *testing.T) {
	clk := newFakeClock()
	base := clk.now()
	root := NewRoot(clk.now, base.Add(50*time.Millisecond))

	const branches = 16
	var kids []*Scope
	var leaves []*Scope
	for range branches {
		mid := root.Child(time.Time{})
		for range 4 {
			leaf := mid.Child(time.Time{})
			leaf.OnDone(func() {})
			leaves = append(leaves, leaf)
		}
		kids = append(kids, mid)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			root.Cancel("race")
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clk.advance(time.Second)
			root.Tick()
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := root.Child(time.Time{})
			<-c.Done()
			c.OnDone(func() {})
		}()
	}
	wg.Wait()

	assertEnded(t, root)
	for i, k := range kids {
		assertEnded(t, k)
		err := k.Err()
		if !errors.Is(err, ErrAncestorEnded) && !errors.Is(err, ErrDeadlineExceeded) {
			t.Fatalf("kid %d Err = %v", i, err)
		}
	}
	for i, leaf := range leaves {
		assertEnded(t, leaf)
		err := leaf.Err()
		if !errors.Is(err, ErrAncestorEnded) && !errors.Is(err, ErrDeadlineExceeded) &&
			!errors.Is(err, ErrCanceled) {
			t.Fatalf("leaf %d unexpected Err = %v", i, err)
		}
		if leaf.Origin() == nil {
			t.Fatalf("leaf %d Origin must be set", i)
		}
	}
}
