package inodeledger

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, P, K, L int) *Ledger {
	t.Helper()
	l, err := New(P, K, L)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", P, K, L, err)
	}
	return l
}

func mustCreate(t *testing.T, l *Ledger, b int) int {
	t.Helper()
	id, err := l.Create(b)
	if err != nil {
		t.Fatalf("Create(%d): %v", b, err)
	}
	return id
}

func mustOpen(t *testing.T, l *Ledger, i int) int {
	t.Helper()
	h, err := l.Open(i)
	if err != nil {
		t.Fatalf("Open(%d): %v", i, err)
	}
	return h
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want errors.Is(%v, %v) to hold", err, target)
	}
}

func checkOrphans(t *testing.T, l *Ledger, want []int) {
	t.Helper()
	got := l.Orphans()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
}

func checkUsed(t *testing.T, l *Ledger, want int) {
	t.Helper()
	if got := l.Used(); got != want {
		t.Fatalf("Used = %d, want %d", got, want)
	}
}

// 题目给出的完整示例：Crash 分支。
func TestSpecExampleCrash(t *testing.T) {
	l := mustNew(t, 100, 3, 5)
	mustCreate(t, l, 10)
	mustCreate(t, l, 10)
	mustCreate(t, l, 10)
	mustOK(t, l.BeginShrink(1, 4))
	checkOrphans(t, l, []int{1})
	checkUsed(t, l, 30)
	h := mustOpen(t, l, 2)
	if h != 1 {
		t.Fatalf("handle = %d, want 1", h)
	}
	mustOK(t, l.Unlink(2))
	checkOrphans(t, l, []int{2, 1})
	mustOK(t, l.BeginShrink(2, 2))
	checkOrphans(t, l, []int{2, 1})
	mustOK(t, l.Unlink(3))
	checkUsed(t, l, 20)
	if _, ok := l.InfoOf(3); ok {
		t.Fatal("inode 3 should be deleted")
	}
	got := l.Crash()
	want := []CrashRecord{{Inode: 2, Action: ActionDelete}, {Inode: 1, Action: ActionTruncate}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Crash = %v, want %v", got, want)
	}
	checkUsed(t, l, 4)
	checkOrphans(t, l, []int{})
	info, ok := l.InfoOf(1)
	if !ok || info.Blocks != 4 || info.Links != 1 || info.Pending {
		t.Fatalf("inode 1 after crash = %+v, ok=%v", info, ok)
	}
}

// 题目给出的完整示例：FinishShrink 分支。
func TestSpecExampleFinish(t *testing.T) {
	l := mustNew(t, 100, 3, 5)
	mustCreate(t, l, 10)
	mustCreate(t, l, 10)
	mustCreate(t, l, 10)
	mustOK(t, l.BeginShrink(1, 4))
	h := mustOpen(t, l, 2)
	mustOK(t, l.Unlink(2))
	mustOK(t, l.BeginShrink(2, 2))
	mustOK(t, l.Unlink(3))
	mustOK(t, l.FinishShrink(1))
	checkOrphans(t, l, []int{2})
	checkUsed(t, l, 14)
	mustOK(t, l.FinishShrink(2))
	checkOrphans(t, l, []int{2})
	checkUsed(t, l, 6)
	mustOK(t, l.Close(h))
	checkOrphans(t, l, []int{})
	checkUsed(t, l, 4)
	if _, ok := l.InfoOf(2); ok {
		t.Fatal("inode 2 should be deleted after last close")
	}
}

func TestUnlinkToZeroWithOpen(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 5)
	mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	checkOrphans(t, l, []int{1})
	checkUsed(t, l, 5)
	info, ok := l.InfoOf(1)
	if !ok || info.Links != 0 || info.Opens != 1 || info.Blocks != 5 {
		t.Fatalf("inode 1 = %+v, ok=%v", info, ok)
	}
}

func TestUnlinkToZeroWithoutOpen(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 5)
	mustOK(t, l.Unlink(1))
	checkOrphans(t, l, []int{})
	checkUsed(t, l, 0)
	if _, ok := l.InfoOf(1); ok {
		t.Fatal("inode 1 should be deleted")
	}
	mustErrIs(t, l.Unlink(1), ErrNotFound)
}

func TestCrashReclaimsInReverseOrder(t *testing.T) {
	l := mustNew(t, 30, 3, 2)
	for i := 0; i < 3; i++ {
		mustCreate(t, l, 3)
		mustOpen(t, l, i+1)
		mustOK(t, l.Unlink(i+1))
	}
	checkOrphans(t, l, []int{3, 2, 1})
	got := l.Crash()
	want := []CrashRecord{
		{Inode: 3, Action: ActionDelete},
		{Inode: 2, Action: ActionDelete},
		{Inode: 1, Action: ActionDelete},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Crash = %v, want %v", got, want)
	}
	checkUsed(t, l, 0)
	checkOrphans(t, l, []int{})
}

func TestCloseMiddleOrphanKeepsOrder(t *testing.T) {
	l := mustNew(t, 30, 3, 2)
	handles := make([]int, 3)
	for i := 0; i < 3; i++ {
		mustCreate(t, l, 3)
		handles[i] = mustOpen(t, l, i+1)
		mustOK(t, l.Unlink(i+1))
	}
	checkOrphans(t, l, []int{3, 2, 1})
	mustOK(t, l.Close(handles[1]))
	checkOrphans(t, l, []int{3, 1})
	checkUsed(t, l, 6)
	got := l.Crash()
	want := []CrashRecord{
		{Inode: 3, Action: ActionDelete},
		{Inode: 1, Action: ActionDelete},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Crash = %v, want %v", got, want)
	}
}

func TestMultipleHandlesLastCloseReleases(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 5)
	h1 := mustOpen(t, l, 1)
	h2 := mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	mustOK(t, l.Close(h1))
	checkOrphans(t, l, []int{1})
	checkUsed(t, l, 5)
	mustOK(t, l.Close(h2))
	checkOrphans(t, l, []int{})
	checkUsed(t, l, 0)
	if _, ok := l.InfoOf(1); ok {
		t.Fatal("inode 1 should be deleted after last close")
	}
}

func TestShrinkOnOrphanReturnsBlocks(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 8)
	mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	checkOrphans(t, l, []int{1})
	mustOK(t, l.Shrink(1, 3))
	checkUsed(t, l, 3)
	info, _ := l.InfoOf(1)
	if info.Blocks != 3 {
		t.Fatalf("blocks = %d, want 3", info.Blocks)
	}
	checkOrphans(t, l, []int{1})
}

func TestLinkAndOpenOnOrphanRejected(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 5)
	mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	mustErrIs(t, l.Link(1), ErrNoLinks)
	if _, err := l.Open(1); !errors.Is(err, ErrNoLinks) {
		t.Fatalf("Open on orphan: %v", err)
	}
	mustErrIs(t, l.Unlink(1), ErrNoLinks)
	info, _ := l.InfoOf(1)
	if info.Links != 0 || info.Opens != 1 {
		t.Fatalf("state changed by rejected ops: %+v", info)
	}
}

func TestOrphanListFull(t *testing.T) {
	l := mustNew(t, 20, 1, 3)
	mustCreate(t, l, 4)
	mustCreate(t, l, 4)
	mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	checkOrphans(t, l, []int{1})

	// Unlink 需要首次进入链表但被拒，状态不变。
	mustOpen(t, l, 2)
	mustErrIs(t, l.Unlink(2), ErrOrphanListFull)
	info, _ := l.InfoOf(2)
	if info.Links != 1 || info.Opens != 1 {
		t.Fatalf("inode 2 changed by rejected Unlink: %+v", info)
	}
	checkOrphans(t, l, []int{1})
	checkUsed(t, l, 8)

	// BeginShrink 需要首次进入链表同样被拒。
	mustErrIs(t, l.BeginShrink(2, 1), ErrOrphanListFull)
	info, _ = l.InfoOf(2)
	if info.Pending {
		t.Fatal("rejected BeginShrink registered a pending shrink")
	}

	// 已在链表中的 inode 不受容量限制。
	mustOK(t, l.BeginShrink(1, 2))
	checkOrphans(t, l, []int{1})
	info, _ = l.InfoOf(1)
	if !info.Pending || info.PendingBlocks != 2 {
		t.Fatalf("inode 1 pending = %+v", info)
	}
}

func TestDualConditionPositionUnchanged(t *testing.T) {
	l := mustNew(t, 20, 3, 2)
	mustCreate(t, l, 6)
	mustCreate(t, l, 6)
	mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	mustOpen(t, l, 2)
	mustOK(t, l.Unlink(2))
	checkOrphans(t, l, []int{2, 1})

	// inode 1 已在链表，BeginShrink 后位置不变。
	mustOK(t, l.BeginShrink(1, 2))
	checkOrphans(t, l, []int{2, 1})
	checkUsed(t, l, 12)

	// FinishShrink 后仍满足「链接 0 且打开大于 0」，留在原位。
	mustOK(t, l.FinishShrink(1))
	checkOrphans(t, l, []int{2, 1})
	checkUsed(t, l, 8)
	info, _ := l.InfoOf(1)
	if info.Blocks != 2 || info.Pending || !info.InOrphanList {
		t.Fatalf("inode 1 = %+v", info)
	}
}

func TestBeginShrinkThenUnlinkToZeroDeletes(t *testing.T) {
	l := mustNew(t, 20, 2, 2)
	mustCreate(t, l, 6)
	mustCreate(t, l, 6)
	mustOpen(t, l, 2)
	mustOK(t, l.Unlink(2))
	mustOK(t, l.BeginShrink(1, 2))
	checkOrphans(t, l, []int{1, 2})

	// Unlink 到 0 且无打开：连同登记与链表位置一起删除。
	mustOK(t, l.Unlink(1))
	checkOrphans(t, l, []int{2})
	checkUsed(t, l, 6)
	if _, ok := l.InfoOf(1); ok {
		t.Fatal("inode 1 should be deleted")
	}
	mustErrIs(t, l.FinishShrink(1), ErrNotFound)
}

func TestCrashTruncateKeepsInode(t *testing.T) {
	l := mustNew(t, 20, 2, 2)
	mustCreate(t, l, 9)
	mustOK(t, l.BeginShrink(1, 3))
	got := l.Crash()
	want := []CrashRecord{{Inode: 1, Action: ActionTruncate}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Crash = %v, want %v", got, want)
	}
	info, ok := l.InfoOf(1)
	if !ok || info.Blocks != 3 || info.Links != 1 || info.Pending || info.InOrphanList {
		t.Fatalf("inode 1 after crash = %+v, ok=%v", info, ok)
	}
	checkUsed(t, l, 3)
	checkOrphans(t, l, []int{})
}

func TestLinkLimitExact(t *testing.T) {
	l := mustNew(t, 10, 1, 2)
	mustCreate(t, l, 1)
	mustOK(t, l.Link(1))
	info, _ := l.InfoOf(1)
	if info.Links != 2 {
		t.Fatalf("links = %d, want 2", info.Links)
	}
	mustErrIs(t, l.Link(1), ErrLinkLimit)
	info, _ = l.InfoOf(1)
	if info.Links != 2 {
		t.Fatalf("links changed by rejected Link: %+v", info)
	}
}

func TestCrashInvalidatesHandles(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	mustCreate(t, l, 5)
	h := mustOpen(t, l, 1)
	mustOK(t, l.Unlink(1))
	l.Crash()
	mustErrIs(t, l.Close(h), ErrNotFound)
}

func TestRejectionOrdering(t *testing.T) {
	l := mustNew(t, 10, 2, 2)

	// 参数非法排在不存在之前。
	mustErrIs(t, l.Shrink(999, -1), ErrInvalidArgument)
	mustErrIs(t, l.BeginShrink(999, -1), ErrInvalidArgument)
	mustErrIs(t, l.Shrink(999, 1), ErrNotFound)
	mustErrIs(t, l.BeginShrink(999, 1), ErrNotFound)

	mustCreate(t, l, 5)
	// nb 大于现有块数报参数非法，且在 inode 存在后才判。
	mustErrIs(t, l.Shrink(1, 6), ErrInvalidArgument)
	mustErrIs(t, l.BeginShrink(1, 6), ErrInvalidArgument)

	// 无待完成截断。
	mustErrIs(t, l.FinishShrink(1), ErrNoPendingShrink)

	// 截断进行中：Shrink 与 BeginShrink 均被拒。
	mustOK(t, l.BeginShrink(1, 2))
	mustErrIs(t, l.Shrink(1, 1), ErrShrinkInProgress)
	mustErrIs(t, l.BeginShrink(1, 1), ErrShrinkInProgress)
	mustOK(t, l.FinishShrink(1))

	// 句柄不存在 / 已失效。
	mustErrIs(t, l.Close(999), ErrNotFound)
	h := mustOpen(t, l, 1)
	mustOK(t, l.Close(h))
	mustErrIs(t, l.Close(h), ErrNotFound)

	// Create 参数非法与空间不足。
	if _, err := l.Create(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create(-1): %v", err)
	}
	if _, err := l.Create(9); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("Create(9): %v", err)
	}
}

func TestNewRejectsInvalidParams(t *testing.T) {
	for _, args := range [][3]int{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {-1, 1, 1}, {1, -1, 1}, {1, 1, -1}} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New%v: %v", args, err)
		}
	}
}

func TestRejectedOpsDoNotConsumeIDs(t *testing.T) {
	l := mustNew(t, 10, 2, 2)
	if _, err := l.Create(-1); err == nil {
		t.Fatal("Create(-1) should fail")
	}
	if _, err := l.Create(11); err == nil {
		t.Fatal("Create(11) should fail")
	}
	id := mustCreate(t, l, 3)
	if id != 1 {
		t.Fatalf("first inode id = %d, want 1", id)
	}
	if _, err := l.Open(999); err == nil {
		t.Fatal("Open(999) should fail")
	}
	h := mustOpen(t, l, 1)
	if h != 1 {
		t.Fatalf("first handle = %d, want 1", h)
	}
	mustOK(t, l.Close(h))
	mustErrIs(t, l.Close(h), ErrNotFound)
	h2 := mustOpen(t, l, 1)
	if h2 != 2 {
		t.Fatalf("second handle = %d, want 2", h2)
	}
}

func TestConcurrentSmoke(t *testing.T) {
	l := mustNew(t, 64, 4, 4)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				i := (seed+n)%4 + 1
				switch n % 9 {
				case 0:
					l.Create(1)
				case 1:
					l.Link(i)
				case 2:
					l.Unlink(i)
				case 3:
					if h, err := l.Open(i); err == nil {
						l.Close(h)
					}
				case 4:
					l.Shrink(i, 0)
				case 5:
					l.BeginShrink(i, 0)
				case 6:
					l.FinishShrink(i)
				case 7:
					l.Crash()
				default:
					l.Used()
					l.Orphans()
					l.InfoOf(i)
				}
			}
		}(w)
	}
	wg.Wait()
	if got := l.Used(); got < 0 || got > 64 {
		t.Fatalf("Used out of range: %d", got)
	}
	if len(l.Orphans()) > 4 {
		t.Fatalf("orphan list longer than capacity")
	}
}
