package walreclaim

import (
	"errors"
	"reflect"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func mustErrIs(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got error %v, want %v", ctx, got, want)
	}
}

// w 恰等于某表 first 时不可回收；当前日志永不可回收。
func TestFirstBoundaryAndCurrentLog(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"), "CreateCF(a)")

	// 首次写入：表 first = cur = 1。
	mustOK(t, m.Write("a"), "Write@1")
	if got := m.Roll(); got != 2 {
		t.Fatalf("Roll = %d, want 2", got)
	}
	mustOK(t, m.Write("a"), "Write@2")

	// 唯一未落盘表 first=1：可回收要求 w < 1，故 w=1 也不可回收；
	// 同时 w < cur=2 排除了当前日志 2。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete = %v, want empty (w==first and current log blocked)", got)
	}

	mustOK(t, m.FlushStart("a"), "FlushStart")
	// 冻结表在 FlushDone 之前仍阻碍回收：其 first=1，w=1 恰等于 first 不可回收。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete after FlushStart = %v, want empty (frozen first=1 blocks)", got)
	}

	// 冻结表在 FlushDone 之前仍阻碍回收：重新 FlushStart 前先写入会复用 cur 语义，
	// 这里直接验证冻结表 first=1 仍在 first 多重集合中（1 已因冻结表阻碍本就不可回收）。
	cur, purged, firsts := snapshotOf(m)
	if cur != 2 || purged != 0 || !reflect.DeepEqual(firsts, []int64{1}) {
		t.Fatalf("snapshot = cur %d purged %d firsts %v, want 2 0 [1]", cur, purged, firsts)
	}

	mustOK(t, m.FlushDone("a"), "FlushDone")
	if got := m.Obsolete(); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("Obsolete after FlushDone = %v, want [1] (current log 2 still blocked)", got)
	}
}

// 冻结后新建的活跃表在下一次写入时才取当时的 cur。
func TestNewActiveTakesFirstOnNextWrite(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"), "CreateCF(a)")
	mustOK(t, m.Write("a"), "Write@1") // 活跃表 first=1
	m.Roll()                           // cur=2
	mustOK(t, m.FlushStart("a"), "FlushStart freezes first=1")

	// 冻结后活跃表为空；仅 Roll 不写入，新活跃表不应“预取” first。
	m.Roll() // cur=3
	if _, _, firsts := snapshotOf(m); !reflect.DeepEqual(firsts, []int64{1}) {
		t.Fatalf("firsts after Roll-without-write = %v, want [1]", firsts)
	}
	// 冻结表 first=1 尚未 FlushDone，w=1 恰等于 first，不可回收。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete = %v, want empty (frozen first=1 still blocks)", got)
	}

	mustOK(t, m.Write("a"), "Write@3 creates new active")
	_, _, firsts := snapshotOf(m)
	if !reflect.DeepEqual(firsts, []int64{1, 3}) {
		t.Fatalf("firsts after new write = %v, want [1 3]", firsts)
	}
	// 冻结表 first=1 仍在，minFirst=1，全部被挡住。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete = %v, want empty (frozen first=1 remains)", got)
	}
	// 冻结表落盘后，仅剩新活跃表 first=3：w 严格小于 3，[1 2] 可回收。
	mustOK(t, m.FlushDone("a"), "FlushDone frozen first=1")
	if got := m.Obsolete(); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("Obsolete after FlushDone = %v, want [1 2]", got)
	}
}

// 长期不写的列族其旧 first 持续阻碍回收。
func TestIdleCFBlocksReclaim(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"), "CreateCF(a)")
	mustOK(t, m.CreateCF("b"), "CreateCF(b)")
	mustOK(t, m.Write("a"), "a.Write@1") // a 活跃表 first=1
	for i := 0; i < 5; i++ {
		m.Roll()
	}
	mustOK(t, m.Write("b"), "b.Write@6") // b 活跃表 first=6
	m.Roll()                             // cur=7

	// a 长期不再写入，first=1 仍然是全局最小值：
	// 严格小于 1 的编号不存在，故没有任何可回收日志。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete = %v, want empty (idle CF first=1 blocks all)", got)
	}
}

// DropCF 解除阻碍；Purge 之后 Obsolete 不重复。
func TestDropCFUnblocksAndPurgeNoRepeat(t *testing.T) {
	m := NewManager()
	mustOK(t, m.CreateCF("a"), "CreateCF(a)")
	mustOK(t, m.CreateCF("b"), "CreateCF(b)")
	mustOK(t, m.Write("a"), "a.Write@1")
	m.Roll()
	mustOK(t, m.Write("b"), "b.Write@2")
	m.Roll()
	mustOK(t, m.FlushStart("a"), "freeze a first=1") // a 仅剩冻结表 first=1
	m.Roll()                                         // cur=4

	// minFirst=1，没有可回收编号。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete before DropCF = %v, want empty", got)
	}

	mustOK(t, m.DropCF("a"), "DropCF(a)")
	// a 的活跃/冻结表全部作废；仅剩 b.first=2，可回收 w 需 w < 2 且 w < 4：[1]。
	if got := m.Obsolete(); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("Obsolete after DropCF = %v, want [1]", got)
	}

	if got := m.Purge(); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("Purge = %v, want [1]", got)
	}
	// Purge 之后同样集合不重复出现。
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete after Purge = %v, want empty (no repeats)", got)
	}
	if got := m.Purge(); len(got) != 0 {
		t.Fatalf("second Purge = %v, want empty", got)
	}

	// 待 b 落盘后，新释放的编号从 2 开始接续，不重复 1。
	mustOK(t, m.FlushStart("b"), "FlushStart(b)")
	mustOK(t, m.FlushDone("b"), "FlushDone(b)")
	if got := m.Obsolete(); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("Obsolete after b flushed = %v, want [2 3]", got)
	}
}

// 拒绝原因按“空名 → 重名/不存在 → 特定前置条件”顺序只报第一个；
// 被拒绝的操作不改变任何状态。
func TestRejectionOrderingAndAtomicity(t *testing.T) {
	m := NewManager()

	// 空名优先于一切其它原因。
	mustErrIs(t, m.CreateCF(""), ErrEmptyName, "CreateCF(empty)")
	mustErrIs(t, m.Write(""), ErrEmptyName, "Write(empty)")
	mustErrIs(t, m.FlushStart(""), ErrEmptyName, "FlushStart(empty)")
	mustErrIs(t, m.FlushDone(""), ErrEmptyName, "FlushDone(empty)")
	mustErrIs(t, m.DropCF(""), ErrEmptyName, "DropCF(empty)")

	// 不存在先于“活跃表为空/冻结队列为空”。
	mustErrIs(t, m.Write("x"), ErrNotFound, "Write(missing)")
	mustErrIs(t, m.FlushStart("x"), ErrNotFound, "FlushStart(missing)")
	mustErrIs(t, m.FlushDone("x"), ErrNotFound, "FlushDone(missing)")
	mustErrIs(t, m.DropCF("x"), ErrNotFound, "DropCF(missing)")

	mustOK(t, m.CreateCF("a"), "CreateCF(a)")
	mustErrIs(t, m.CreateCF("a"), ErrExists, "CreateCF(existing)")

	// 空名甚至优先于重名判定。
	mustErrIs(t, m.CreateCF(""), ErrEmptyName, "CreateCF(empty) again")

	// FlushStart 活跃表为空；FlushDone 冻结队列为空。
	mustErrIs(t, m.FlushStart("a"), ErrEmptyActive, "FlushStart(empty active)")
	mustErrIs(t, m.FlushDone("a"), ErrEmptyFlushed, "FlushDone(empty frozen)")

	// 被拒绝操作后状态不变：写入仍使 first=1（而非别的 cur）。
	mustOK(t, m.Write("a"), "Write after rejections")
	cur, purged, firsts := snapshotOf(m)
	if cur != 1 || purged != 0 || !reflect.DeepEqual(firsts, []int64{1}) {
		t.Fatalf("state changed by rejected ops: cur %d purged %d firsts %v", cur, purged, firsts)
	}

	// FlushDone 空队列的拒绝发生在 FlushStart 成功之后也不改动状态。
	mustOK(t, m.FlushStart("a"), "FlushStart(a)")
	mustOK(t, m.FlushDone("a"), "FlushDone")
	mustErrIs(t, m.FlushDone("a"), ErrEmptyFlushed, "FlushDone again")

	// DropCF 后名字视为不存在，可重新创建为全新列族。
	mustOK(t, m.DropCF("a"), "DropCF")
	mustErrIs(t, m.Write("a"), ErrNotFound, "Write after DropCF")
	mustOK(t, m.CreateCF("a"), "recreate a")
	if cur, _, firsts := snapshotOf(m); cur != 1 || len(firsts) != 0 {
		t.Fatalf("recreated CF not fresh: cur %d firsts %v", cur, firsts)
	}
}

// 没有任何未落盘表时不受 first 限制（只剩 w < cur 与未 Purge）。
func TestNoMemtablesUnlimited(t *testing.T) {
	m := NewManager()
	m.Roll()
	m.Roll()
	m.Roll() // cur=4
	if got := m.Obsolete(); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("Obsolete = %v, want [1 2 3]", got)
	}
	if got := m.Purge(); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("Purge = %v, want [1 2 3]", got)
	}
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete = %v, want empty", got)
	}
}
