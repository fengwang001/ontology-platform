package heap

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, p, c int) *Table {
	t.Helper()
	tb, err := New(p, c)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", p, c, err)
	}
	return tb
}

func mustInsert(t *testing.T, tb *Table, key string, xid int64) (int, int) {
	t.Helper()
	p, s, err := tb.Insert(key, xid)
	if err != nil {
		t.Fatalf("Insert(%q, %d): %v", key, xid, err)
	}
	return p, s
}

func mustSnapshot(t *testing.T, tb *Table, s int64) int {
	t.Helper()
	id, err := tb.Snapshot(s)
	if err != nil {
		t.Fatalf("Snapshot(%d): %v", s, err)
	}
	return id
}

func TestNewInvalid(t *testing.T) {
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidPageCount) {
		t.Fatalf("New(0,1) err = %v", err)
	}
	if _, err := New(1, 0); !errors.Is(err, ErrInvalidSlotCount) {
		t.Fatalf("New(1,0) err = %v", err)
	}
	if _, err := New(1, 1); err != nil {
		t.Fatalf("New(1,1) err = %v", err)
	}
}

// xmin 恰等于 h 不置位，等于 h-1 置位。
func TestVacuumXminBoundary(t *testing.T) {
	tb := mustNew(t, 2, 2)
	mustInsert(t, tb, "a", 10) // 页0: xmin=10
	mustInsert(t, tb, "b", 9)  // 页0: xmin=9

	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	if tb.AllVisible(0) {
		t.Fatal("xmin=10 == h=10 的行存在时不应置位")
	}

	tb2 := mustNew(t, 1, 1)
	mustInsert(t, tb2, "a", 9)
	if err := tb2.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	if !tb2.AllVisible(0) {
		t.Fatal("xmin=9 == h-1 时应置位")
	}
}

// xmax 恰等于 h 的行不被移除且位为假；xmax 小于 h 的被移除。
func TestVacuumXmaxBoundary(t *testing.T) {
	tb := mustNew(t, 2, 2)
	p0, s0 := mustInsert(t, tb, "a", 1)
	_, _ = p0, s0
	mustInsert(t, tb, "b", 1)
	if err := tb.Delete(0, 0, 10); err != nil { // xmax = 10 == h
		t.Fatal(err)
	}
	if err := tb.Delete(0, 1, 9); err != nil { // xmax = 9 < h
		t.Fatal(err)
	}
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	// xmax=9 的行被物理移除，槽位变空，Insert 应复用最小空槽 (0,1)。
	p, s, err := tb.Insert("c", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p != 0 || s != 1 {
		t.Fatalf("xmax<h 的行未被移除，Insert 落在 (%d,%d)，期望 (0,1)", p, s)
	}
	// xmax=10 的行仍在且 xmax!=0，位为假。
	if tb.AllVisible(0) {
		t.Fatal("xmax==h 的行不应被移除，位应为假")
	}
	res, err := tb.Scan("a", "b", mustSnapshot(t, tb, 100))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("xmax=10 的行对 s=100 不可见，却产出了 %v", res.Rows)
	}
}

// Delete 即使 xmax 不小于 h 也清位。
func TestDeleteClearsBit(t *testing.T) {
	tb := mustNew(t, 1, 1)
	mustInsert(t, tb, "a", 1)
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	if !tb.AllVisible(0) {
		t.Fatal("Vacuum 后应置位")
	}
	if err := tb.Delete(0, 0, 100); err != nil { // xmax=100 >= h=10
		t.Fatal(err)
	}
	if tb.AllVisible(0) {
		t.Fatal("Delete 后位应被清除")
	}
}

// Insert 清位后再次扫描回表次数增加。
func TestInsertClearsBitAndFetchesIncrease(t *testing.T) {
	tb := mustNew(t, 1, 2)
	mustInsert(t, tb, "a", 1)
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	snap := mustSnapshot(t, tb, 10)
	res1, err := tb.Scan("a", "z", snap)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Fetches != 0 || res1.Skips != 1 {
		t.Fatalf("置位后扫描: fetches=%d skips=%d，期望 0/1", res1.Fetches, res1.Skips)
	}
	mustInsert(t, tb, "b", 2) // 清位
	res2, err := tb.Scan("a", "z", snap)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Fetches != 2 || res2.Skips != 0 {
		t.Fatalf("Insert 清位后扫描: fetches=%d skips=%d，期望 2/0", res2.Fetches, res2.Skips)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("产出 %v，期望 2 行", res2.Rows)
	}
}

// 快照值恰等于 h 时 Vacuum 允许，h-1 时拒绝。
func TestVacuumSnapshotBoundary(t *testing.T) {
	tb := mustNew(t, 1, 1)
	mustInsert(t, tb, "a", 1)
	snap := mustSnapshot(t, tb, 10)
	if err := tb.Vacuum(0, 11); !errors.Is(err, ErrSnapshotBlocking) {
		t.Fatalf("快照值 10 < h=11 应拒绝: Vacuum(11) err = %v，期望 ErrSnapshotBlocking", err)
	}
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatalf("快照值恰等于 h 应允许: %v", err)
	}
	if err := tb.Release(snap); err != nil {
		t.Fatal(err)
	}
}

// Snapshot 小于全局最大已用 h 被拒，且被拒调用不占号。
func TestSnapshotBelowMaxHRejected(t *testing.T) {
	tb := mustNew(t, 1, 1)
	mustInsert(t, tb, "a", 1)
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Snapshot(9); !errors.Is(err, ErrSnapshotTooOld) {
		t.Fatalf("Snapshot(9) err = %v，期望 ErrSnapshotTooOld", err)
	}
	if _, err := tb.Snapshot(0); !errors.Is(err, ErrInvalidSnapshotValue) {
		t.Fatalf("Snapshot(0) err = %v，期望 ErrInvalidSnapshotValue", err)
	}
	id := mustSnapshot(t, tb, 10) // 恰等于 maxH 允许
	if id != 1 {
		t.Fatalf("被拒的 Snapshot 不应占号，首个成功编号 = %d，期望 1", id)
	}
}

// 空页置位。
func TestVacuumEmptyPageSetsBit(t *testing.T) {
	tb := mustNew(t, 2, 1)
	if err := tb.Vacuum(1, 5); err != nil {
		t.Fatal(err)
	}
	if !tb.AllVisible(1) {
		t.Fatal("空页 Vacuum 后应置位")
	}
}

// 各类拒绝不得改变任何状态。
func TestRejectedOpsKeepState(t *testing.T) {
	tb := mustNew(t, 1, 2)
	mustInsert(t, tb, "a", 5)
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	snap := mustSnapshot(t, tb, 10)
	before, err := tb.Scan("a", "z", snap)
	if err != nil {
		t.Fatal(err)
	}
	visBefore := tb.AllVisible(0)
	maxHBefore := tb.MaxH()

	// Insert: xid 非正
	if _, _, err := tb.Insert("x", 0); !errors.Is(err, ErrInvalidXid) {
		t.Fatalf("Insert xid=0 err = %v", err)
	}
	// Delete: xid 非正 / 越界 / 空槽 / 已有 xmax
	if err := tb.Delete(0, 0, -1); !errors.Is(err, ErrInvalidXid) {
		t.Fatalf("Delete xid<0 err = %v", err)
	}
	if err := tb.Delete(1, 0, 1); !errors.Is(err, ErrSlotOutOfRange) {
		t.Fatalf("Delete 页越界 err = %v", err)
	}
	if err := tb.Delete(0, 1, 1); !errors.Is(err, ErrSlotEmpty) {
		t.Fatalf("Delete 空槽 err = %v", err)
	}
	if err := tb.Delete(0, 0, 7); err != nil {
		t.Fatal(err)
	}
	if err := tb.Delete(0, 0, 8); !errors.Is(err, ErrAlreadyDeleted) {
		t.Fatalf("重复 Delete err = %v", err)
	}
	// Snapshot: 非正 / 小于 maxH
	if _, err := tb.Snapshot(0); !errors.Is(err, ErrInvalidSnapshotValue) {
		t.Fatalf("Snapshot(0) err = %v", err)
	}
	if _, err := tb.Snapshot(5); !errors.Is(err, ErrSnapshotTooOld) {
		t.Fatalf("Snapshot(5) err = %v", err)
	}
	// Release: 未知编号
	if err := tb.Release(999); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("Release(999) err = %v", err)
	}
	// Vacuum: h 非正 / 页越界 / 快照阻塞
	if err := tb.Vacuum(0, 0); !errors.Is(err, ErrInvalidHorizon) {
		t.Fatalf("Vacuum h=0 err = %v", err)
	}
	if err := tb.Vacuum(9, 100); !errors.Is(err, ErrPageOutOfRange) {
		t.Fatalf("Vacuum 页越界 err = %v", err)
	}
	if err := tb.Vacuum(0, 11); !errors.Is(err, ErrSnapshotBlocking) {
		t.Fatalf("Vacuum 被快照阻塞 err = %v", err)
	}
	// Scan: lo>hi / 快照未知
	if _, err := tb.Scan("z", "a", snap); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("Scan lo>hi err = %v", err)
	}
	if _, err := tb.Scan("a", "z", 999); !errors.Is(err, ErrUnknownSnapshot) {
		t.Fatalf("Scan 未知快照 err = %v", err)
	}

	// 状态未变：可见位、maxH、扫描结果（除一次成功 Delete 外无变化）。
	if tb.MaxH() != maxHBefore {
		t.Fatalf("maxH 变为 %d，期望 %d", tb.MaxH(), maxHBefore)
	}
	// Delete(0,0,7) 是唯一成功操作，它清位。
	if tb.AllVisible(0) != false {
		t.Fatal("成功 Delete 后位应为假")
	}
	_ = visBefore
	after, err := tb.Scan("a", "z", snap)
	if err != nil {
		t.Fatal(err)
	}
	// xmax=7 < s=10，行被过滤，产出为空。
	if len(after.Rows) != 0 {
		t.Fatalf("删除后扫描产出 %v，期望空", after.Rows)
	}
	if len(before.Rows) != 1 {
		t.Fatalf("删除前扫描产出 %v，期望 1 行", before.Rows)
	}
}

// Insert 满表报错；Delete 后槽位不会自动变空（物理移除只由 Vacuum 做）。
func TestInsertFullAndSlotReuse(t *testing.T) {
	tb := mustNew(t, 1, 1)
	p, s, _ := tb.Insert("a", 1)
	if p != 0 || s != 0 {
		t.Fatalf("首行落在 (%d,%d)，期望 (0,0)", p, s)
	}
	if _, _, err := tb.Insert("b", 2); !errors.Is(err, ErrNoFreeSlot) {
		t.Fatalf("满表 Insert err = %v", err)
	}
	if err := tb.Delete(0, 0, 5); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tb.Insert("b", 2); !errors.Is(err, ErrNoFreeSlot) {
		t.Fatalf("Delete 不释放槽位，Insert err = %v", err)
	}
	if err := tb.Vacuum(0, 10); err != nil {
		t.Fatal(err)
	}
	p2, s2, err := tb.Insert("b", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p2 != 0 || s2 != 0 {
		t.Fatalf("Vacuum 后 Insert 落在 (%d,%d)，期望 (0,0)", p2, s2)
	}
}

// Scan 结果按 (key, 页, 槽) 升序，lo<=key<hi。
func TestScanOrderingAndRange(t *testing.T) {
	tb := mustNew(t, 2, 2)
	mustInsert(t, tb, "b", 1)
	mustInsert(t, tb, "a", 1)
	mustInsert(t, tb, "c", 1)
	mustInsert(t, tb, "a", 2) // 同 key 第二行
	snap := mustSnapshot(t, tb, 100)
	res, err := tb.Scan("a", "c", snap)
	if err != nil {
		t.Fatal(err)
	}
	var got []Entry
	for _, e := range res.Rows {
		got = append(got, e)
	}
	want := []Entry{
		{Key: "a", Page: 0, Slot: 1},
		{Key: "a", Page: 1, Slot: 1},
		{Key: "b", Page: 0, Slot: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Scan 产出 %v，期望 %v", got, want)
	}
}
