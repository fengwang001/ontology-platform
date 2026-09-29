package consistency

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// logRead 按统一格式打印操作、两个位点、返回位点与判定依据。
func logRead(t *testing.T, op string, res Result, reason string) {
	t.Helper()
	t.Logf("op=%s commit=%d applied=%d returned=%d target=%d degraded=%v reason=%s",
		op, res.Positions.Commit, res.Positions.Applied,
		res.Positions.Applied, res.Target, res.Degraded, reason)
}

func logPositions(t *testing.T, op string, p Positions, reason string) {
	t.Helper()
	t.Logf("op=%s commit=%d applied=%d reason=%s", op, p.Commit, p.Applied, reason)
}

// advanceTo 将 commit 连续推进到 commit、applied 推进到 applied。
func advanceTo(t *testing.T, r *Reader, commit, applied uint64) {
	t.Helper()
	for i := uint64(1); i <= commit; i++ {
		if err := r.AdvanceCommit(i); err != nil {
			t.Fatalf("AdvanceCommit(%d): %v", i, err)
		}
	}
	if applied > 0 {
		if err := r.MarkApplied(applied); err != nil {
			t.Fatalf("MarkApplied(%d): %v", applied, err)
		}
	}
}

// TestDegradedBoundary 覆盖降级判定边界：
// applied == target 时不降级，applied == target-1 时降级。
func TestDegradedBoundary(t *testing.T) {
	const maxLag = 3
	cases := []struct {
		applied  uint64
		degraded bool
		reason   string
	}{
		{7, false, "applied==target 边界：恰好满足滞后约束，不降级"},
		{8, false, "applied>target：满足滞后约束，不降级"},
		{6, true, "applied==target-1 边界：落后目标一位，降级"},
		{0, true, "applied 远落后目标，降级"},
	}
	for _, c := range cases {
		r := New()
		advanceTo(t, r, 10, 0) // commit=10, target=10-3=7
		if c.applied > 0 {
			if err := r.MarkApplied(c.applied); err != nil {
				t.Fatalf("MarkApplied(%d): %v", c.applied, err)
			}
		}
		res, err := r.Read(maxLag, ModeDegraded)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		logRead(t, "read-degraded", res, c.reason)
		if res.Target != 7 {
			t.Fatalf("target=%d, want 7", res.Target)
		}
		if res.Degraded != c.degraded {
			t.Fatalf("applied=%d: degraded=%v, want %v", c.applied, res.Degraded, c.degraded)
		}
		if res.Positions.Applied != c.applied {
			t.Fatalf("returned=%d, want %d", res.Positions.Applied, c.applied)
		}
	}
}

// TestBlockingWake 阻塞模式：多个等待者被一次推进并发安全地放行。
func TestBlockingWake(t *testing.T) {
	const maxLag = 2
	r := New()
	advanceTo(t, r, 5, 1) // commit=5, target=5-2=3, applied=1 < 3

	const waiters = 8
	var wg sync.WaitGroup
	results := make([]Result, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := r.Read(maxLag, ModeBlocking)
			if err != nil {
				t.Errorf("Read: %v", err)
				return
			}
			results[i] = res
		}(i)
	}

	// 给等待者时间进入阻塞。
	time.Sleep(50 * time.Millisecond)
	if err := r.MarkApplied(3); err != nil {
		t.Fatalf("MarkApplied(3): %v", err)
	}
	// 推进到 4 并继续推进 commit，验证已冻结的 target 不受影响。
	if err := r.MarkApplied(4); err != nil {
		t.Fatalf("MarkApplied(4): %v", err)
	}
	if err := r.AdvanceCommit(6); err != nil {
		t.Fatalf("AdvanceCommit(6): %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("等待者未被全部唤醒")
	}

	for i, res := range results {
		logRead(t, "read-blocking", res, "阻塞至 applied>=target 后被唤醒，target 在调用时刻已冻结")
		if res.Target != 3 {
			t.Fatalf("waiter %d: target=%d, want 3（冻结目标不应随后续 commit 推进改变）", i, res.Target)
		}
		if res.Positions.Applied < res.Target {
			t.Fatalf("waiter %d: returned=%d < target=%d", i, res.Positions.Applied, res.Target)
		}
		if res.Degraded {
			t.Fatalf("waiter %d: 阻塞模式不应报告降级", i)
		}
	}
}

// TestZeroLagStrongConsistency 滞后为零即强一致：阻塞到 applied==commit。
func TestZeroLagStrongConsistency(t *testing.T) {
	r := New()
	advanceTo(t, r, 4, 2) // commit=4, applied=2, target=4

	var wg sync.WaitGroup
	wg.Add(1)
	var res Result
	var err error
	go func() {
		defer wg.Done()
		res, err = r.Read(0, ModeBlocking)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := r.MarkApplied(3); err != nil {
		t.Fatalf("MarkApplied(3): %v", err)
	}
	// applied=3 仍未达 target=4，不应被唤醒。
	time.Sleep(50 * time.Millisecond)
	if p := r.Snapshot(); p.Applied != 3 {
		t.Fatalf("applied=%d, want 3", p.Applied)
	}
	if err := r.MarkApplied(4); err != nil {
		t.Fatalf("MarkApplied(4): %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("强一致读取未在 applied 追平 commit 后返回")
	}
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	logRead(t, "read-zero-lag", res, "maxLag=0 时 target==commit，阻塞至 applied 追平 commit，强一致")
	if res.Positions.Applied != res.Positions.Commit {
		t.Fatalf("强一致要求 returned==commit: applied=%d commit=%d",
			res.Positions.Applied, res.Positions.Commit)
	}
}

// TestRejections 非法操作整体拒绝，且失败不改变位点与等待者状态。
func TestRejections(t *testing.T) {
	r := New()
	advanceTo(t, r, 3, 2) // commit=3, applied=2

	before := r.Snapshot()

	if err := r.AdvanceCommit(5); !errors.Is(err, ErrNonContiguousCommit) {
		t.Fatalf("AdvanceCommit(5): err=%v, want ErrNonContiguousCommit", err)
	}
	logPositions(t, "reject-noncontiguous-commit", r.Snapshot(), "提交跳跃 3->5 不连续，整体拒绝")

	if err := r.AdvanceCommit(3); !errors.Is(err, ErrNonContiguousCommit) {
		t.Fatalf("AdvanceCommit(3): err=%v, want ErrNonContiguousCommit", err)
	}
	logPositions(t, "reject-stale-commit", r.Snapshot(), "重复提交位点 3，非 +1 推进，整体拒绝")

	if err := r.MarkApplied(4); !errors.Is(err, ErrAppliedOutOfRange) {
		t.Fatalf("MarkApplied(4): err=%v, want ErrAppliedOutOfRange", err)
	}
	logPositions(t, "reject-applied-over-commit", r.Snapshot(), "applied=4 越过 commit=3，整体拒绝")

	if err := r.MarkApplied(1); !errors.Is(err, ErrAppliedOutOfRange) {
		t.Fatalf("MarkApplied(1): err=%v, want ErrAppliedOutOfRange", err)
	}
	logPositions(t, "reject-applied-backwards", r.Snapshot(), "applied 回退 2->1，整体拒绝")

	if _, err := r.Read(-1, ModeDegraded); !errors.Is(err, ErrNegativeLag) {
		t.Fatalf("Read(-1): err=%v, want ErrNegativeLag", err)
	}
	logPositions(t, "reject-negative-lag", r.Snapshot(), "滞后上限为负，整体拒绝")

	after := r.Snapshot()
	if before != after {
		t.Fatalf("失败操作改变了位点: before=%+v after=%+v", before, after)
	}
	if err := r.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// 等待者状态不变：阻塞中的读取仍应能被正常唤醒。
	var wg sync.WaitGroup
	wg.Add(1)
	var res Result
	var err error
	go func() {
		defer wg.Done()
		res, err = r.Read(0, ModeBlocking) // target=commit=3
	}()
	time.Sleep(50 * time.Millisecond)
	if err := r.MarkApplied(3); err != nil {
		t.Fatalf("MarkApplied(3): %v", err)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("失败操作影响了等待者状态：阻塞读取未被唤醒")
	}
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	logRead(t, "read-after-rejections", res, "失败操作未改变等待者状态，阻塞读取正常唤醒")
}

// TestConcurrentSnapshotIdentical 并发读到的位点快照必须逐字段相同（同一把锁内读取）。
func TestConcurrentSnapshotIdentical(t *testing.T) {
	r := New()
	advanceTo(t, r, 100, 40)

	var wg sync.WaitGroup
	snaps := make([]Positions, 64)
	for i := range snaps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snaps[i] = r.Snapshot()
		}(i)
	}
	wg.Wait()

	for i, s := range snaps {
		if s != snaps[0] {
			t.Fatalf("snapshot %d 与其他不一致: %+v vs %+v", i, s, snaps[0])
		}
	}
	logPositions(t, "concurrent-snapshot", snaps[0], "无并发推进时所有快照逐字段相同")
}

// TestConcurrentCheckAndRead 位点查询与自检可与推进并发调用（配合 -race）。
func TestConcurrentCheckAndRead(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := r.Check(); err != nil {
					t.Errorf("Check: %v", err)
					return
				}
				p := r.Snapshot()
				if p.Applied > p.Commit {
					t.Errorf("applied=%d > commit=%d", p.Applied, p.Commit)
					return
				}
				if _, err := r.Read(1, ModeDegraded); err != nil {
					t.Errorf("Read: %v", err)
					return
				}
			}
		}()
	}

	for c := uint64(1); c <= 200; c++ {
		if err := r.AdvanceCommit(c); err != nil {
			t.Fatalf("AdvanceCommit(%d): %v", c, err)
		}
		if err := r.MarkApplied(c); err != nil {
			t.Fatalf("MarkApplied(%d): %v", c, err)
		}
	}
	close(stop)
	wg.Wait()
	logPositions(t, "concurrent-advance", r.Snapshot(), "推进与并发查询/自检交错执行，不变量保持")
}

// naive 是朴素参照实现：用显而易见的方式维护位点并回答读取。
type naive struct {
	commit  uint64
	applied uint64
}

func (n *naive) advanceCommit(to uint64) bool {
	if to != n.commit+1 {
		return false
	}
	n.commit = to
	return true
}

func (n *naive) markApplied(to uint64) bool {
	if to <= n.applied || to > n.commit {
		return false
	}
	n.applied = to
	return true
}

func (n *naive) readDegraded(maxLag int64) (uint64, uint64, bool, bool) {
	if maxLag < 0 {
		return 0, 0, false, false
	}
	target := targetFor(n.commit, maxLag)
	return n.applied, target, n.applied < target, true
}

// TestAgainstNaiveReference 用随机操作序列将实现与朴素参照逐步核对。
func TestAgainstNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	r := New()
	ref := &naive{}

	for step := 0; step < 2000; step++ {
		switch rng.Intn(4) {
		case 0: // 提交：一半概率合法 +1，一半概率随机（常非法）
			to := r.commit + 1
			if rng.Intn(2) == 0 {
				to = uint64(rng.Intn(int(r.commit) + 3))
			}
			got := r.AdvanceCommit(to)
			wantOK := ref.advanceCommit(to)
			if (got == nil) != wantOK {
				t.Fatalf("step %d AdvanceCommit(%d): got=%v wantOK=%v", step, to, got, wantOK)
			}
		case 1: // 应用：随机目标，核对越界拒绝
			to := uint64(rng.Intn(int(r.commit) + 3))
			got := r.MarkApplied(to)
			wantOK := ref.markApplied(to)
			if (got == nil) != wantOK {
				t.Fatalf("step %d MarkApplied(%d): got=%v wantOK=%v", step, to, got, wantOK)
			}
		default: // 降级读：核对返回位点、目标与降级判定
			maxLag := int64(rng.Intn(8)) - 2 // 覆盖负滞后
			res, err := r.Read(maxLag, ModeDegraded)
			wantRet, wantTarget, wantDegraded, wantOK := ref.readDegraded(maxLag)
			if (err == nil) != wantOK {
				t.Fatalf("step %d Read(%d): err=%v wantOK=%v", step, maxLag, err, wantOK)
			}
			if err != nil {
				continue
			}
			if res.Positions.Applied != wantRet || res.Target != wantTarget || res.Degraded != wantDegraded {
				t.Fatalf("step %d Read(%d): got=(ret=%d target=%d degraded=%v) want=(%d %d %v)",
					step, maxLag, res.Positions.Applied, res.Target, res.Degraded,
					wantRet, wantTarget, wantDegraded)
			}
		}
		if r.commit != ref.commit || r.applied != ref.applied {
			t.Fatalf("step %d: positions diverged: got=(%d,%d) want=(%d,%d)",
				step, r.commit, r.applied, ref.commit, ref.applied)
		}
	}
	logPositions(t, "naive-reference", r.Snapshot(), "2000 步随机操作与朴素参照逐步核对一致")
}
