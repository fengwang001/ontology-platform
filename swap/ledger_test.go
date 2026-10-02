package swap

import (
	"errors"
	"reflect"
	"testing"
)

func wantAlloc(t *testing.T, l *Ledger, want int) {
	t.Helper()
	s, err := l.Alloc()
	if err != nil {
		t.Fatalf("Alloc() 返回错误 %v，期望槽位 %d", err, want)
	}
	if s != want {
		t.Fatalf("Alloc() = %d，期望 %d", s, want)
	}
}

func wantQueue(t *testing.T, l *Ledger, want ...int) {
	t.Helper()
	got := l.FreeClusters()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FreeClusters() = %v，期望 %v", got, want)
	}
}

func wantCurrent(t *testing.T, l *Ledger, cur, cursor int) {
	t.Helper()
	gotCur, gotCursor := l.Current()
	if gotCur != cur || gotCursor != cursor {
		t.Fatalf("Current() = (%d, %d)，期望 (%d, %d)", gotCur, gotCursor, cur, cursor)
	}
}

func wantInfo(t *testing.T, l *Ledger, s, count int, cache bool) {
	t.Helper()
	gotCount, gotCache := l.Info(s)
	if gotCount != count || gotCache != cache {
		t.Fatalf("Info(%d) = (%d, %v)，期望 (%d, %v)", s, gotCount, gotCache, count, cache)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("错误 = %v，期望 %v", err, want)
	}
}

// stateSnap 是账本完整状态快照，用于校验被拒绝的操作不改变任何状态。
type stateSnap struct {
	used   int
	cur    int
	cursor int
	queue  []int
	counts []int
	caches []bool
}

func snapshot(l *Ledger, n int) stateSnap {
	s := stateSnap{
		used:   l.Used(),
		queue:  l.FreeClusters(),
		counts: make([]int, n),
		caches: make([]bool, n),
	}
	s.cur, s.cursor = l.Current()
	for i := 0; i < n; i++ {
		s.counts[i], s.caches[i] = l.Info(i)
	}
	return s
}

func wantState(t *testing.T, l *Ledger, n int, want stateSnap) {
	t.Helper()
	got := snapshot(l, n)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("状态被改变：得到 %+v，期望 %+v", got, want)
	}
}

// TestSpecExample 完整走查题目示例：N=12、K=4、Max=2，
// 簇 0 为槽位 1..3，簇 1 为 4..7，簇 2 为 8..11。
func TestSpecExample(t *testing.T) {
	l, err := New(12, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantQueue(t, l, 0, 1, 2)
	wantCurrent(t, l, -1, 0)

	// 连续四次 Alloc 得 1、2、3、4。
	wantAlloc(t, l, 1)
	wantCurrent(t, l, 0, 2)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	wantCurrent(t, l, 0, 4)
	// 第 4 次：簇 0 内无编号不小于 4 的空闲槽位，簇 0 并非整簇空闲，
	// 取队首簇 1，cur=簇 1，cursor=5。
	wantAlloc(t, l, 4)
	wantCurrent(t, l, 1, 5)
	wantQueue(t, l, 2)

	// CacheDrop(2) 使槽位 2 空闲；下一次 Alloc 得 5 而不是 2，
	// 即游标之前空出的槽位在 cur 内不会被重用。
	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}
	wantAlloc(t, l, 5)

	// CacheDrop(1)、CacheDrop(3) 后簇 0 整簇空闲且不是 cur，追加到队尾。
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	wantQueue(t, l, 2)
	if err := l.CacheDrop(3); err != nil {
		t.Fatalf("CacheDrop(3): %v", err)
	}
	wantQueue(t, l, 2, 0)

	// Alloc 三次得 6、7、8：第三次簇 1 耗尽，取队首簇 2。
	wantAlloc(t, l, 6)
	wantAlloc(t, l, 7)
	wantAlloc(t, l, 8)
	wantCurrent(t, l, 2, 9)
	wantQueue(t, l, 0)

	// 再 Alloc 三次得 9、10、11。
	wantAlloc(t, l, 9)
	wantAlloc(t, l, 10)
	wantAlloc(t, l, 11)
	wantCurrent(t, l, 2, 12)

	// 再 Alloc 得 1：簇 2 耗尽，取队首簇 0，cursor=2。
	wantAlloc(t, l, 1)
	wantCurrent(t, l, 0, 2)
	wantQueue(t, l)

	// 其后依次得 2、3，之后报 ErrNoSpace。
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}
	if got := l.Used(); got != 11 {
		t.Fatalf("Used() = %d，期望 11", got)
	}
	if got := l.FreeSlots(); got != 0 {
		t.Fatalf("FreeSlots() = %d，期望 0", got)
	}
}

// TestFallbackExample 走查题目回退路径示例：队列为空时
// 取全局最小空闲槽位并改写 cur 与 cursor。
func TestFallbackExample(t *testing.T) {
	l, err := New(12, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 11; i++ {
		wantAlloc(t, l, i+1)
	}
	wantCurrent(t, l, 2, 12)
	wantQueue(t, l)

	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}
	if err := l.CacheDrop(6); err != nil {
		t.Fatalf("CacheDrop(6): %v", err)
	}
	// cur=簇 2、cursor=12，队列为空：走第（3）步得 2，cur=簇 0、cursor=3。
	wantAlloc(t, l, 2)
	wantCurrent(t, l, 0, 3)
	// 簇 0 内编号不小于 3 的槽位 3 在用，再走第（3）步得 6，cur=簇 1、cursor=7。
	wantAlloc(t, l, 6)
	wantCurrent(t, l, 1, 7)
}

// TestSlotZeroNeverAllocated 槽位 0 是交换区头部，永不被分配。
func TestSlotZeroNeverAllocated(t *testing.T) {
	l, err := New(2, 1, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}
	// 回收后重新分配仍只能得到槽位 1。
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	wantAlloc(t, l, 1)
	wantInfo(t, l, 0, 0, false)
}

// TestPartialFirstCluster 首簇不足 K 个可用槽位。
func TestPartialFirstCluster(t *testing.T) {
	// N=4、K=4：唯一的簇 0 只有槽位 1..3，共 3 个（不足 K=4）。
	l, err := New(4, 4, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantQueue(t, l, 0)
	wantAlloc(t, l, 1)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}

	// N=5、K=4：簇 0 为槽位 1..3（不足 K），簇 1 只有槽位 4。
	l2, err := New(5, 4, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantQueue(t, l2, 0, 1)
	wantAlloc(t, l2, 1)
	wantAlloc(t, l2, 2)
	wantAlloc(t, l2, 3)
	wantAlloc(t, l2, 4)
	if _, err := l2.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}
}

// TestK1NoClusterZero K=1 时编号 0 的簇没有任何可用槽位，不存在。
func TestK1NoClusterZero(t *testing.T) {
	l, err := New(4, 1, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 初始队列只含簇 1、2、3，不含簇 0。
	wantQueue(t, l, 1, 2, 3)
	wantAlloc(t, l, 1)
	wantCurrent(t, l, 1, 2)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}
}

// TestCurFullyFreeReselected cur 整簇空闲但游标已到末尾时，
// 该簇被再次选中并从首槽开始。
func TestCurFullyFreeReselected(t *testing.T) {
	l, err := New(8, 4, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	wantAlloc(t, l, 4)
	wantCurrent(t, l, 1, 5)
	wantAlloc(t, l, 5)
	wantAlloc(t, l, 6)
	wantAlloc(t, l, 7)
	wantCurrent(t, l, 1, 8)
	// 释放簇 1 全部槽位：簇 1 整簇空闲但它是 cur，不入队。
	for s := 4; s <= 7; s++ {
		if err := l.CacheDrop(s); err != nil {
			t.Fatalf("CacheDrop(%d): %v", s, err)
		}
	}
	wantQueue(t, l)
	// 游标已到簇 1 末尾：cur 出队再入队后被重新选中，从首槽 4 开始。
	wantAlloc(t, l, 4)
	wantCurrent(t, l, 1, 5)
	wantQueue(t, l)
}

// TestCurEnqueuedWhenReplaced cur 自己变整簇空闲时不入队，
// 在被替换时入队。
func TestCurEnqueuedWhenReplaced(t *testing.T) {
	l, err := New(8, 4, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	wantAlloc(t, l, 4)
	// 释放簇 0：整簇空闲且不是 cur，立即入队。
	for s := 1; s <= 3; s++ {
		if err := l.CacheDrop(s); err != nil {
			t.Fatalf("CacheDrop(%d): %v", s, err)
		}
	}
	wantQueue(t, l, 0)
	wantAlloc(t, l, 5)
	wantAlloc(t, l, 6)
	wantAlloc(t, l, 7)
	// 释放簇 1（即 cur）全部槽位：不入队。
	for s := 4; s <= 7; s++ {
		if err := l.CacheDrop(s); err != nil {
			t.Fatalf("CacheDrop(%d): %v", s, err)
		}
	}
	wantQueue(t, l, 0)
	// 下一次 Alloc 替换 cur：旧 cur（簇 1）整簇空闲，追加到队尾；
	// 随后取队首簇 0。
	wantAlloc(t, l, 1)
	wantCurrent(t, l, 0, 2)
	wantQueue(t, l, 1)
}

// TestRefCountBounds count 恰到 Max 与差 1 的行为。
func TestRefCountBounds(t *testing.T) {
	// Max=2：0 -> 1（差 1）-> 2（恰到 Max）-> 溢出。
	l, err := New(4, 2, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantInfo(t, l, 1, 0, true)
	if err := l.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	wantInfo(t, l, 1, 1, true)
	if err := l.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	wantInfo(t, l, 1, 2, true)
	wantErr(t, l.Dup(1), ErrOverflow)
	wantInfo(t, l, 1, 2, true)

	// Max=1：第一次 Dup 即到上限。
	l1, err := New(4, 2, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l1, 1)
	if err := l1.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	wantInfo(t, l1, 1, 1, true)
	wantErr(t, l1.Dup(1), ErrOverflow)
}

// TestCacheOnlyFreeUnderflow 仅由缓存持有的槽位 Free 报下溢。
func TestCacheOnlyFreeUnderflow(t *testing.T) {
	l, err := New(4, 2, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantInfo(t, l, 1, 0, true)
	wantErr(t, l.Free(1), ErrUnderflow)
	wantInfo(t, l, 1, 0, true)
}

// TestReclaimNeedsBothZero 缓存与计数各自归零才回收，
// 两种次序都验证。
func TestReclaimNeedsBothZero(t *testing.T) {
	l, err := New(8, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 先 CacheDrop 后 Free。
	wantAlloc(t, l, 1)
	if err := l.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	if got := l.Used(); got != 1 {
		t.Fatalf("CacheDrop 后 Used() = %d，期望 1（count 仍持有）", got)
	}
	if err := l.Free(1); err != nil {
		t.Fatalf("Free(1): %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("Free 后 Used() = %d，期望 0", got)
	}

	// 反过来：先 Free 后 CacheDrop。
	// 此时 cur=簇 0、cursor=2，按下一适应规则分配到槽位 2。
	wantAlloc(t, l, 2)
	if err := l.Dup(2); err != nil {
		t.Fatalf("Dup(2): %v", err)
	}
	if err := l.Free(2); err != nil {
		t.Fatalf("Free(2): %v", err)
	}
	if got := l.Used(); got != 1 {
		t.Fatalf("Free 后 Used() = %d，期望 1（cache 仍持有）", got)
	}
	wantErr(t, l.Free(2), ErrUnderflow)
	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("CacheDrop 后 Used() = %d，期望 0", got)
	}
}

// TestForkReleaseDuplicates Fork 与 Release 中重复槽位的
// 累计判定与整批回滚。
func TestForkReleaseDuplicates(t *testing.T) {
	l, err := New(8, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	if err := l.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	wantInfo(t, l, 1, 1, true)

	// count=1 时 Fork([s,s])：第二个元素推演到 count=Max，报 (1, ErrOverflow)，
	// 整批不生效。
	idx, err := l.Fork([]int{1, 1})
	if idx != 1 || !errors.Is(err, ErrOverflow) {
		t.Fatalf("Fork([1,1]) = (%d, %v)，期望 (1, ErrOverflow)", idx, err)
	}
	wantInfo(t, l, 1, 1, true)

	// count=1、cache 为假时 Release([s,s])：推演中第一次已使槽位空闲，
	// 第二次报 (1, ErrNotInUse)。
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	idx, err = l.Release([]int{1, 1})
	if idx != 1 || !errors.Is(err, ErrNotInUse) {
		t.Fatalf("Release([1,1]) = (%d, %v)，期望 (1, ErrNotInUse)", idx, err)
	}
	wantInfo(t, l, 1, 1, false)

	// cache 为真时 Release([s,s]) 报 (1, ErrUnderflow)。
	if err := l.CacheAdd(1); err != nil {
		t.Fatalf("CacheAdd(1): %v", err)
	}
	idx, err = l.Release([]int{1, 1})
	if idx != 1 || !errors.Is(err, ErrUnderflow) {
		t.Fatalf("Release([1,1]) = (%d, %v)，期望 (1, ErrUnderflow)", idx, err)
	}
	wantInfo(t, l, 1, 1, true)

	// 整批成功：Fork([1]) 后 count=2，Release([1,1]) 后 count=0。
	if idx, err := l.Fork([]int{1}); idx != -1 || err != nil {
		t.Fatalf("Fork([1]) = (%d, %v)，期望 (-1, nil)", idx, err)
	}
	wantInfo(t, l, 1, 2, true)
	if idx, err := l.Release([]int{1, 1}); idx != -1 || err != nil {
		t.Fatalf("Release([1,1]) = (%d, %v)，期望 (-1, nil)", idx, err)
	}
	wantInfo(t, l, 1, 0, true)

	// 失败批中的越界元素同样使整批不生效。
	before := snapshot(l, 8)
	idx, err = l.Fork([]int{1, 99})
	if idx != 1 || !errors.Is(err, ErrRange) {
		t.Fatalf("Fork([1,99]) = (%d, %v)，期望 (1, ErrRange)", idx, err)
	}
	wantState(t, l, 8, before)
}

// TestReleaseEnqueueOrder 批量释放时簇入队次序等于元素释放次序。
func TestReleaseEnqueueOrder(t *testing.T) {
	l, err := New(12, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	all := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	for range all {
		if _, err := l.Alloc(); err != nil {
			t.Fatalf("Alloc: %v", err)
		}
	}
	if idx, err := l.Fork(all); idx != -1 || err != nil {
		t.Fatalf("Fork(all) = (%d, %v)", idx, err)
	}
	for _, s := range all {
		if err := l.CacheDrop(s); err != nil {
			t.Fatalf("CacheDrop(%d): %v", s, err)
		}
	}
	wantQueue(t, l)
	// 按 8..11、4..7、1..3 的次序释放：簇 2 是 cur 不入队，
	// 簇 1、簇 0 依次入队。
	if idx, err := l.Release([]int{8, 9, 10, 11, 4, 5, 6, 7, 1, 2, 3}); idx != -1 || err != nil {
		t.Fatalf("Release = (%d, %v)", idx, err)
	}
	wantQueue(t, l, 1, 0)
	// 簇 2（cur）此刻整簇空闲：下一次 Alloc 替换 cur 时它入队，
	// 并取队首簇 1。
	wantAlloc(t, l, 4)
	wantCurrent(t, l, 1, 5)
	wantQueue(t, l, 0, 2)
}

// TestNoSpaceKeepsState 满池时 ErrNoSpace 不改任何状态。
func TestNoSpaceKeepsState(t *testing.T) {
	l, err := New(4, 2, 1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	before := snapshot(l, 4)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc() 错误 = %v，期望 ErrNoSpace", err)
	}
	wantState(t, l, 4, before)
}

// TestErrorPrecedence 各类错误按 ErrRange、ErrNotInUse、
// 操作特定错误的先后判定。
func TestErrorPrecedence(t *testing.T) {
	l, err := New(8, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)

	// ErrRange 优先于一切。
	for _, bad := range []int{-1, 0, 8, 100} {
		wantErr(t, l.Dup(bad), ErrRange)
		wantErr(t, l.Free(bad), ErrRange)
		wantErr(t, l.CacheAdd(bad), ErrRange)
		wantErr(t, l.CacheDrop(bad), ErrRange)
	}
	// 槽位 2 空闲：ErrNotInUse 优先于操作特定错误。
	wantErr(t, l.Dup(2), ErrNotInUse)
	wantErr(t, l.Free(2), ErrNotInUse)
	wantErr(t, l.CacheAdd(2), ErrNotInUse)
	wantErr(t, l.CacheDrop(2), ErrNotInUse)

	// 槽位 1：count=0、cache 为真。
	wantErr(t, l.Free(1), ErrUnderflow)
	wantErr(t, l.CacheAdd(1), ErrExists)
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	// 此刻槽位 1 已空闲（count=0、cache 为假）。
	wantErr(t, l.CacheDrop(1), ErrNotInUse)
	// 按下一适应规则分配到槽位 2，构造 count=1、cache 为假的在用槽位。
	wantAlloc(t, l, 2)
	if err := l.Dup(2); err != nil {
		t.Fatalf("Dup(2): %v", err)
	}
	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}
	wantErr(t, l.CacheDrop(2), ErrNoCache)
	if err := l.Dup(2); err != nil {
		t.Fatalf("Dup(2): %v", err)
	}
	wantErr(t, l.Dup(2), ErrOverflow)
}

// TestRejectedOpsKeepState 被拒绝的操作不得改变任何槽位、
// cur、cursor 与队列。
func TestRejectedOpsKeepState(t *testing.T) {
	l, err := New(12, 4, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantAlloc(t, l, 1)
	wantAlloc(t, l, 2)
	wantAlloc(t, l, 3)
	wantAlloc(t, l, 4)
	if err := l.Dup(1); err != nil {
		t.Fatalf("Dup(1): %v", err)
	}
	if err := l.CacheDrop(1); err != nil {
		t.Fatalf("CacheDrop(1): %v", err)
	}
	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}

	rejected := []func() error{
		func() error { return l.Dup(0) },
		func() error { return l.Dup(12) },
		func() error { return l.Dup(2) }, // 槽位 2 空闲
		func() error { return l.Free(0) },
		func() error { return l.Free(2) },
		func() error { return l.Free(3) }, // 仅缓存持有
		func() error { return l.CacheAdd(0) },
		func() error { return l.CacheAdd(2) },
		func() error { return l.CacheAdd(3) }, // cache 已为真
		func() error { return l.CacheDrop(0) },
		func() error { return l.CacheDrop(2) },
		func() error { return l.CacheDrop(1) }, // cache 已为假
		func() error { _, err := l.Fork([]int{1, 1, 1}); return err },
		func() error { _, err := l.Release([]int{1, 1, 1}); return err },
	}
	for i, op := range rejected {
		before := snapshot(l, 12)
		if err := op(); err == nil {
			t.Fatalf("第 %d 个操作本应被拒绝", i)
		}
		wantState(t, l, 12, before)
	}
}

// TestConfigValidation 构造参数越界整体拒绝。
func TestConfigValidation(t *testing.T) {
	bad := [][3]int{
		{1, 1, 1}, {0, 1, 1}, {-5, 1, 1},
		{1_000_001, 1, 1},
		{2, 0, 1}, {2, -1, 1}, {2, 1025, 1},
		{2, 1, 0}, {2, 1, -1}, {2, 1, 256},
	}
	for _, p := range bad {
		if _, err := New(p[0], p[1], p[2]); !errors.Is(err, ErrConfig) {
			t.Fatalf("New%v 错误 = %v，期望 ErrConfig", p, err)
		}
	}
	good := [][3]int{
		{2, 1, 1},
		{1_000_000, 1024, 255},
		{2, 1024, 1},
	}
	for _, p := range good {
		if _, err := New(p[0], p[1], p[2]); err != nil {
			t.Fatalf("New%v 意外失败: %v", p, err)
		}
	}
}
