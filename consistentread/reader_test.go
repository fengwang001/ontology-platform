package consistentread

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// logOp 按要求打印：操作、提交位点、已应用位点、返回位点与判定依据。
func logOp(t *testing.T, op string, committed, applied Position, snap Snapshot) {
	t.Helper()
	t.Logf("op=%s committed={e%d,i%d} applied={e%d,i%d} returned={e%d,i%d} degraded=%t reason=%q",
		op, committed.Epoch, committed.Index, applied.Epoch, applied.Index,
		snap.Position.Epoch, snap.Position.Index, snap.Degraded, snap.Reason)
}

// naiveReader 是与实现无关的朴素参照：单 goroutine、不加锁，
// 只用最小规则推导期望结果，供测试逐字段核对。
type naiveReader struct {
	committed int64
	applied   int64
	started   bool
}

func (n *naiveReader) commit(i int64) {
	n.committed = i
	n.started = true
}

func (n *naiveReader) apply(i int64) { n.applied = i }

func (n *naiveReader) degrade(maxLag int64) (pos int64, degraded bool) {
	if n.committed-n.applied > maxLag {
		return n.applied, true
	}
	return n.applied, false
}

func (n *naiveReader) block(target int64) int64 {
	// 阻塞模式只返回冻结的调用时刻提交位点。
	return target
}

func advanceN(t *testing.T, r *Reader, c, a int64) {
	t.Helper()
	for i := int64(0); i < c; i++ {
		if err := r.AdvanceCommit(Position{Epoch: 1, Index: i}); err != nil {
			t.Fatalf("commit i=%d: %v", i, err)
		}
	}
	for i := int64(0); i < a; i++ {
		if err := r.AdvanceApply(Position{Epoch: 1, Index: i}); err != nil {
			t.Fatalf("apply i=%d: %v", i, err)
		}
	}
}

func TestDegradeBoundaries(t *testing.T) {
	// 已提交 0..4（committed.Index=4），已应用 0..2（applied.Index=2），滞后 = 2。
	// 判定边界：maxLag=1 -> 降级；maxLag=2 -> 刚好满足，不降级；maxLag=3 -> 不降级。
	for _, tc := range []struct {
		maxLag       int64
		wantDegraded bool
	}{
		{1, true},
		{2, false},
		{3, false},
	} {
		r := NewReader()
		advanceN(t, r, 5, 3)

		snap, err := r.Read(ModeDegrade, tc.maxLag)
		if err != nil {
			t.Fatalf("maxLag=%d: %v", tc.maxLag, err)
		}
		c, a := r.Positions()
		logOp(t, fmt.Sprintf("degrade maxLag=%d", tc.maxLag), c, a, snap)

		if snap.Degraded != tc.wantDegraded {
			t.Fatalf("maxLag=%d degraded=%v want %v", tc.maxLag, snap.Degraded, tc.wantDegraded)
		}
		if snap.Position != a {
			t.Fatalf("maxLag=%d returned %v want applied %v", tc.maxLag, snap.Position, a)
		}

		// 朴素参照核对。
		n := &naiveReader{committed: 4, applied: 2, started: true}
		wantPos, wantDeg := n.degrade(tc.maxLag)
		if snap.Position.Index != wantPos || snap.Degraded != wantDeg {
			t.Fatalf("naive mismatch: got (%d,%v) want (%d,%v)",
				snap.Position.Index, snap.Degraded, wantPos, wantDeg)
		}
	}
}

func TestStrongConsistencyZeroLag(t *testing.T) {
	// maxLag=0 且已应用 == 已提交：强一致，不降级；滞后 > 0 时必须降级而不是返回旧提交。
	r := NewReader()
	advanceN(t, r, 3, 3)

	snap, err := r.Read(ModeDegrade, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, a := r.Positions()
	logOp(t, "degrade maxLag=0 strong", c, a, snap)
	if snap.Degraded || snap.Position != c || c != a {
		t.Fatalf("want strong read at %v, got %v degraded=%v", c, snap.Position, snap.Degraded)
	}

	if err := r.AdvanceCommit(Position{Epoch: 1, Index: 3}); err != nil {
		t.Fatal(err)
	}
	snap, err = r.Read(ModeDegrade, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, a = r.Positions()
	logOp(t, "degrade maxLag=0 behind", c, a, snap)
	if !snap.Degraded || snap.Position != a {
		t.Fatalf("want degraded read at applied %v, got %v", a, snap.Position)
	}
}

func TestBlockFreezesTargetAndWakes(t *testing.T) {
	r := NewReader()
	advanceN(t, r, 3, 1) // committed i=2, applied i=0

	type result struct {
		snap Snapshot
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		snap, err := r.Read(ModeBlock, 0)
		resCh <- result{snap, err}
	}()

	// 等待阻塞读真正进入等待。
	time.Sleep(50 * time.Millisecond)

	// 在阻塞期间提交继续前进到 i=4：冻结目标应保持为 i=2。
	if err := r.AdvanceCommit(Position{Epoch: 1, Index: 3}); err != nil {
		t.Fatal(err)
	}
	if err := r.AdvanceCommit(Position{Epoch: 1, Index: 4}); err != nil {
		t.Fatal(err)
	}

	// 只应用到冻结目标 i=2，就应放行，而不应要求追到最新提交 i=4。
	if err := r.AdvanceApply(Position{Epoch: 1, Index: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.AdvanceApply(Position{Epoch: 1, Index: 2}); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatal(res.err)
		}
		c, a := r.Positions()
		logOp(t, "block wake", c, a, res.snap)
		if res.snap.Degraded {
			t.Fatal("block read must never be degraded")
		}
		if got, want := res.snap.Position, (Position{Epoch: 1, Index: 2}); got != want {
			t.Fatalf("frozen target=%v want %v (committed moved on)", got, want)
		}
		if res.snap.Position == c {
			t.Fatalf("frozen target must not follow committed %v", c)
		}
		// 朴素参照核对：返回的是调用时刻冻结的目标。
		n := &naiveReader{}
		if res.snap.Position.Index != n.block(2) {
			t.Fatal("naive block mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked reader was not woken")
	}
}

func TestBlockWakesMultipleWaiters(t *testing.T) {
	r := NewReader()
	advanceN(t, r, 2, 0) // committed i=1, applied 未开始

	const waiters = 16
	var wg sync.WaitGroup
	wg.Add(waiters)
	errs := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			defer wg.Done()
			snap, err := r.Read(ModeBlock, 0)
			if err != nil {
				errs <- err
				return
			}
			if snap.Position != (Position{Epoch: 1, Index: 1}) || snap.Degraded {
				errs <- fmt.Errorf("bad snap %+v", snap)
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	advanceN(t, r, 0, 2) // apply i=0,i=1，应一次性广播放行全部等待者

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("not all waiters were released")
	}
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	c, a := r.Positions()
	logOp(t, "block multi-waiter", c, a, Snapshot{Position: c})
}

func TestRejectionsDoNotMutate(t *testing.T) {
	r := NewReader()
	advanceN(t, r, 3, 2) // committed i=2, applied i=1
	beforeC, beforeA := r.Positions()

	cases := []struct {
		name   string
		fn     func() error
		reason FailureReason
	}{
		{"commit gap", func() error { return r.AdvanceCommit(Position{Epoch: 1, Index: 5}) }, ReasonCommitNotContiguous},
		{"commit backward", func() error { return r.AdvanceCommit(Position{Epoch: 1, Index: 1}) }, ReasonCommitNotAdvanced},
		{"commit epoch regress", func() error { return r.AdvanceCommit(Position{Epoch: 0, Index: 3}) }, ReasonCommitNotAdvanced},
		{"apply ahead", func() error { return r.AdvanceApply(Position{Epoch: 1, Index: 9}) }, ReasonApplyAheadOfCommit},
		{"apply out of order", func() error { return r.AdvanceApply(Position{Epoch: 1, Index: 1}) }, ReasonApplyOutOfOrder},
		{"apply unknown epoch", func() error { return r.AdvanceApply(Position{Epoch: 7, Index: 2}) }, ReasonUnknownPosition},
		{"negative lag", func() error {
			_, err := r.Read(ModeDegrade, -1)
			return err
		}, ReasonNegativeLag},
	}

	for _, tc := range cases {
		err := tc.fn()
		var fail *Failure
		if !errors.As(err, &fail) {
			t.Fatalf("%s: want *Failure, got %v", tc.name, err)
		}
		if fail.Reason != tc.reason {
			t.Fatalf("%s: reason=%q want %q", tc.name, fail.Reason, tc.reason)
		}
		c, a := r.Positions()
		if c != beforeC || a != beforeA {
			t.Fatalf("%s mutated state: c=%v a=%v want c=%v a=%v", tc.name, c, a, beforeC, beforeA)
		}
		if err := r.Check(); err != nil {
			t.Fatalf("%s: self-check after failure: %v", tc.name, err)
		}
		t.Logf("op=reject case=%s committed={e%d,i%d} applied={e%d,i%d} reason=%q",
			tc.name, c.Epoch, c.Index, a.Epoch, a.Index, fail.Reason)
	}
}

func TestConcurrentReadsAreFieldConsistent(t *testing.T) {
	r := NewReader()

	const workers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 一个生产者连续提交并应用。
	wg.Add(1)
	go func() {
		defer wg.Done()
		var i int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := r.AdvanceCommit(Position{Epoch: 1, Index: i}); err == nil {
				_ = r.AdvanceApply(Position{Epoch: 1, Index: i})
				i++
			}
		}
	}()

	// 并发查询与自检：Positions 必须返回逐字段一致的成对位点。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, a := r.Positions()
				if less(c, a) {
					t.Errorf("applied %v ahead of committed %v", a, c)
					return
				}
				if err := r.Check(); err != nil {
					t.Errorf("check: %v", err)
					return
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	c, a := r.Positions()
	logOp(t, "concurrent stress", c, a, Snapshot{Position: c})
}
