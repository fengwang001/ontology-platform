package redolog

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustBuffer(t *testing.T, l0, b, cap, mx uint64) *Buffer {
	t.Helper()
	buf, err := NewBuffer(l0, b, cap, mx)
	if err != nil {
		t.Fatalf("NewBuffer(%d, %d, %d, %d): %v", l0, b, cap, mx, err)
	}
	return buf
}

func mustReserve(t *testing.T, buf *Buffer, n uint64) (uint64, uint64) {
	t.Helper()
	start, end, err := buf.Reserve(n)
	if err != nil {
		t.Fatalf("Reserve(%d): %v", n, err)
	}
	return start, end
}

func mustComplete(t *testing.T, buf *Buffer, start uint64) {
	t.Helper()
	if err := buf.Complete(start); err != nil {
		t.Fatalf("Complete(%d): %v", start, err)
	}
}

func mustWait(t *testing.T, buf *Buffer, w int64, end uint64) bool {
	t.Helper()
	sat, err := buf.Wait(w, end)
	if err != nil {
		t.Fatalf("Wait(%d, %d): %v", w, end, err)
	}
	return sat
}

type flushResult struct {
	blocks uint64
	fd     uint64
	woke   []int64
}

func doFlush(t *testing.T, buf *Buffer, force bool) flushResult {
	t.Helper()
	blocks, fd, woke := buf.Flush(force)
	return flushResult{blocks: blocks, fd: fd, woke: woke}
}

func checkFlush(t *testing.T, buf *Buffer, force bool, want flushResult) {
	t.Helper()
	got := doFlush(t, buf, force)
	if got.blocks != want.blocks || got.fd != want.fd ||
		!reflect.DeepEqual(got.woke, want.woke) {
		t.Fatalf("Flush(%v) = %+v, want %+v", force, got, want)
	}
}

func checkState(t *testing.T, buf *Buffer, r, fd, ready, total uint64) {
	t.Helper()
	if buf.R() != r || buf.Fd() != fd || buf.Ready() != ready ||
		buf.TotalBlocks() != total {
		t.Fatalf("state = (R=%d Fd=%d Ready=%d Total=%d), want (R=%d Fd=%d Ready=%d Total=%d)",
			buf.R(), buf.Fd(), buf.Ready(), buf.TotalBlocks(), r, fd, ready, total)
	}
}

// 较晚区间先完成时 Ready 不前进；最早区间完成后一次越过多个已完成区间。
func TestReadyOutOfOrderCompletion(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 100) // [0,100)
	mustReserve(t, buf, 500) // [100,600)
	mustReserve(t, buf, 30)  // [600,630)

	mustComplete(t, buf, 100)
	mustComplete(t, buf, 600)
	if got := buf.Ready(); got != 0 {
		t.Fatalf("Ready = %d, want 0 (earliest interval incomplete)", got)
	}
	mustComplete(t, buf, 0)
	if got := buf.Ready(); got != 630 {
		t.Fatalf("Ready = %d, want 630 (skip over completed prefix)", got)
	}
}

// 非强制刷盘向下取整到块边界；不足一块时什么都不写。
func TestFlushNonForceRoundsDown(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	start, _ := mustReserve(t, buf, 100)
	mustComplete(t, buf, start)
	if got := buf.Ready(); got != 100 {
		t.Fatalf("Ready = %d, want 100", got)
	}
	// floor(100/512)*512 = 0 <= Fd: no-op.
	checkFlush(t, buf, false, flushResult{blocks: 0, fd: 0})
	checkState(t, buf, 100, 0, 100, 0)

	_, end := mustReserve(t, buf, 500)
	mustComplete(t, buf, 100)
	if got := buf.Ready(); got != end {
		t.Fatalf("Ready = %d, want %d", got, end)
	}
	// floor(600/512)*512 = 512: one full block.
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512})
	checkState(t, buf, 600, 512, 600, 1)
}

// 强制刷盘写出残块；残块续写后再次刷盘按公式重复计入该块。
func TestFlushForcePartialBlockAndRewrite(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 630)
	mustComplete(t, buf, 0)
	// ceil(630/512) - floor(0/512) = 2: the second block is partial.
	checkFlush(t, buf, true, flushResult{blocks: 2, fd: 630})

	mustReserve(t, buf, 370)
	mustComplete(t, buf, 630)
	if got := buf.Ready(); got != 1000 {
		t.Fatalf("Ready = %d, want 1000", got)
	}
	// ceil(1000/512) - floor(630/512) = 2 - 1 = 1: block 2 counted again.
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 1000})
	checkState(t, buf, 1000, 1000, 1000, 3)
}

// target 恰等于 Fd 时无操作。
func TestFlushTargetEqualsFdIsNoop(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 512)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512})
	// Ready == Fd: both force and non-force targets equal Fd.
	checkFlush(t, buf, false, flushResult{blocks: 0, fd: 512})
	checkFlush(t, buf, true, flushResult{blocks: 0, fd: 512})
	checkState(t, buf, 512, 512, 512, 1)
}

// 块数超过 Mx 时按块边界截断：force 也截断、不写残块、唤醒只看截断后的 Fd。
func TestFlushTruncatesAtMx(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 2)
	mustReserve(t, buf, 2000)
	mustComplete(t, buf, 0)
	mustWait(t, buf, 1, 2000)
	// 4 blocks > Mx=2: truncate to (0+2)*512 = 1024, no partial block.
	checkFlush(t, buf, true, flushResult{blocks: 2, fd: 1024})
	// 4 - 2 = 2 <= Mx: reach Ready = 2000, waiter wakes now.
	checkFlush(t, buf, true, flushResult{blocks: 2, fd: 2000, woke: []int64{1}})
	checkState(t, buf, 2000, 2000, 2000, 4)
}

// Mx=1 逐块推进。
func TestFlushMxOneBlockPerFlush(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 4096, 1)
	mustReserve(t, buf, 1536)
	mustComplete(t, buf, 0)
	for i := uint64(1); i <= 3; i++ {
		checkFlush(t, buf, false, flushResult{blocks: 1, fd: i * 512})
	}
	checkFlush(t, buf, false, flushResult{blocks: 0, fd: 1536})
	checkState(t, buf, 1536, 1536, 1536, 3)
}

// L0 不是 B 的倍数时的块数公式。
func TestFlushUnalignedL0(t *testing.T) {
	buf := mustBuffer(t, 100, 512, 2048, 8)
	mustReserve(t, buf, 500) // [100,600)
	mustComplete(t, buf, 100)
	if got := buf.Ready(); got != 600 {
		t.Fatalf("Ready = %d, want 600", got)
	}
	// target = floor(600/512)*512 = 512; blocks = 1 - floor(100/512)=0 = 1.
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512})
	// force: target = 600; blocks = ceil(600/512) - floor(512/512) = 2-1 = 1.
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 600})
	checkState(t, buf, 600, 600, 600, 2)
}

// 等待者唤醒次序：end 升序优先，同 end 按登记序号升序。
func TestWaiterWakeOrder(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 100)
	mustReserve(t, buf, 500)
	mustReserve(t, buf, 30)
	mustComplete(t, buf, 0)
	mustComplete(t, buf, 100)
	mustComplete(t, buf, 600)

	mustWait(t, buf, 7, 630) // registered first, larger end
	mustWait(t, buf, 3, 600)
	mustWait(t, buf, 5, 600) // same end as w3, later seq
	mustWait(t, buf, 9, 100)

	// Non-force flush to 512 wakes only the end=100 waiter.
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512, woke: []int64{9}})
	// Force flush to 630: end 600 pair by registration order, then end 630.
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 630, woke: []int64{3, 5, 7}})
	checkState(t, buf, 630, 630, 630, 2)
}

// end 恰等于 Fd 或已落盘的终点直接返回已满足，不登记。
func TestWaitSatisfiedAtFd(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 512)
	mustReserve(t, buf, 88)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512})

	if sat := mustWait(t, buf, 1, 512); !sat {
		t.Fatal("Wait(1, 512) should be satisfied at Fd=512")
	}
	// w1 was not registered: it may register for a non-durable end.
	if sat := mustWait(t, buf, 1, 600); sat {
		t.Fatal("Wait(1, 600) should register, Fd=512 < 600")
	}
	mustComplete(t, buf, 512)
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 600, woke: []int64{1}})
}

// Wait 错误：负编号、终点不存在、重复登记；被拒后状态不变。
func TestWaitRejections(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 100)

	if _, err := buf.Wait(-1, 100); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Wait(-1, 100) err = %v, want ErrInvalidParam", err)
	}
	if _, err := buf.Wait(1, 101); !errors.Is(err, ErrEndNotFound) {
		t.Fatalf("Wait(1, 101) err = %v, want ErrEndNotFound", err)
	}
	mustWait(t, buf, 1, 100)
	if _, err := buf.Wait(1, 100); !errors.Is(err, ErrDuplicateWaiter) {
		t.Fatalf("Wait(1, 100) again err = %v, want ErrDuplicateWaiter", err)
	}
	checkState(t, buf, 100, 0, 0, 0)
	// The registered waiter is still pending and wakes exactly once.
	mustComplete(t, buf, 0)
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 100, woke: []int64{1}})
	checkFlush(t, buf, true, flushResult{blocks: 0, fd: 100})
}

// Reserve 容量边界：R-Fd 恰等于 Cap 允许，多 1 被拒；刷盘腾空间后再成功。
func TestReserveCapacityBoundary(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 630)
	if _, _, err := buf.Reserve(1500); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("Reserve(1500) err = %v, want ErrBufferFull (630+1500-0 > 2048)", err)
	}
	mustComplete(t, buf, 0)
	checkFlush(t, buf, true, flushResult{blocks: 2, fd: 630})
	// 630 + 1500 - 630 = 1500 <= 2048 now.
	mustReserve(t, buf, 1500)

	// Exactly Cap: 2130 - 630 = 1500 used, 548 more reaches Cap exactly.
	mustReserve(t, buf, 548)
	if got := buf.R() - buf.Fd(); got != 2048 {
		t.Fatalf("R-Fd = %d, want 2048", got)
	}
	if _, _, err := buf.Reserve(1); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("Reserve(1) at full buffer err = %v, want ErrBufferFull", err)
	}
}

// Reserve 的 n 非法；Complete 的区间不存在与重复完成；拒绝不改状态。
func TestRejectionsKeepState(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	mustWait(t, buf, 1, 100)
	snapshot := func() [4]uint64 {
		return [4]uint64{buf.R(), buf.Fd(), buf.Ready(), buf.TotalBlocks()}
	}
	before := snapshot()

	for _, n := range []uint64{0, 2049} {
		if _, _, err := buf.Reserve(n); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Reserve(%d) err = %v, want ErrInvalidParam", n, err)
		}
	}
	if err := buf.Complete(50); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("Complete(50) err = %v, want ErrIntervalNotFound", err)
	}
	if err := buf.Complete(0); !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("Complete(0) again err = %v, want ErrAlreadyComplete", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("state changed by rejected ops: %v -> %v", before, after)
	}
	// The pending waiter survived the rejected operations.
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 100, woke: []int64{1}})
}

// 构造参数越界统一以参数非法拒绝。
func TestConstructorValidation(t *testing.T) {
	cases := []struct{ l0, b, cap, mx uint64 }{
		{1_000_000_000_001, 512, 2048, 1},
		{0, 0, 2048, 1},
		{0, 65537, 2048, 1},
		{0, 512, 0, 1},
		{0, 512, 1_000_000_001, 1},
		{0, 512, 2048, 0},
		{0, 512, 2048, 1_000_001},
	}
	for _, c := range cases {
		if _, err := NewBuffer(c.l0, c.b, c.cap, c.mx); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("NewBuffer(%v) err = %v, want ErrInvalidParam", c, err)
		}
	}
	if _, err := NewBuffer(1_000_000_000_000, 65536, 1_000_000_000, 1_000_000); err != nil {
		t.Fatalf("boundary NewBuffer err = %v", err)
	}
}

// 题目第一段示例的完整回放。
func TestSpecWalkthrough(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 2)
	mustReserve(t, buf, 100)
	mustReserve(t, buf, 500)
	mustReserve(t, buf, 30)
	mustComplete(t, buf, 100)
	mustComplete(t, buf, 600)
	if got := buf.Ready(); got != 0 {
		t.Fatalf("Ready = %d, want 0", got)
	}
	mustComplete(t, buf, 0)
	if got := buf.Ready(); got != 630 {
		t.Fatalf("Ready = %d, want 630", got)
	}
	mustWait(t, buf, 1, 600)
	mustWait(t, buf, 2, 630)
	mustWait(t, buf, 3, 100)
	checkFlush(t, buf, false, flushResult{blocks: 1, fd: 512, woke: []int64{3}})
	checkFlush(t, buf, true, flushResult{blocks: 1, fd: 630, woke: []int64{1, 2}})
	checkFlush(t, buf, false, flushResult{blocks: 0, fd: 630})
	// Buffer space freed by the flushes: Reserve(1500) now succeeds.
	mustReserve(t, buf, 1500)
	checkState(t, buf, 2130, 630, 630, 2)
}

// 并发调用等价于某个串行顺序：不变量始终成立。
func TestConcurrentAccess(t *testing.T) {
	buf := mustBuffer(t, 0, 64, 4096, 4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				start, end, err := buf.Reserve(8)
				if err != nil {
					buf.Flush(i%2 == 0)
					continue
				}
				if err := buf.Complete(start); err != nil {
					t.Errorf("Complete(%d): %v", start, err)
				}
				buf.Wait(int64(g*1000+i), end)
				buf.Flush(i%3 == 0)
			}
		}(g)
	}
	wg.Wait()
	if buf.Fd() > buf.Ready() || buf.Ready() > buf.R() {
		t.Fatalf("invariant broken: Fd=%d Ready=%d R=%d", buf.Fd(), buf.Ready(), buf.R())
	}
	if buf.R()-buf.Fd() > 4096 {
		t.Fatalf("capacity invariant broken: R-Fd=%d", buf.R()-buf.Fd())
	}
}
