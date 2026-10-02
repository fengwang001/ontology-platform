package compactor

import (
	"errors"
	"reflect"
	"testing"
)

func mustPut(t *testing.T, c *Compactor, key string, value int64) uint64 {
	t.Helper()
	seq, err := c.Put(key, value)
	if err != nil {
		t.Fatalf("Put(%q,%d): %v", key, value, err)
	}
	return seq
}

func mustMerge(t *testing.T, c *Compactor, key string, delta int64) uint64 {
	t.Helper()
	seq, err := c.Merge(key, delta)
	if err != nil {
		t.Fatalf("Merge(%q,%d): %v", key, delta, err)
	}
	return seq
}

func mustDelete(t *testing.T, c *Compactor, key string) uint64 {
	t.Helper()
	seq, err := c.Delete(key)
	if err != nil {
		t.Fatalf("Delete(%q): %v", key, err)
	}
	return seq
}

func wantGet(t *testing.T, c *Compactor, key string, s uint64, wantV int64, wantOK bool) {
	t.Helper()
	v, ok, err := c.Get(key, s)
	if err != nil {
		t.Fatalf("Get(%q,%d): %v", key, s, err)
	}
	if v != wantV || ok != wantOK {
		t.Fatalf("Get(%q,%d) = (%d,%v); want (%d,%v)", key, s, v, ok, wantV, wantOK)
	}
}

func wantRecords(t *testing.T, c *Compactor, want []Record) {
	t.Helper()
	got := c.Records()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Records() = %v; want %v", got, want)
	}
}

func wantStats(t *testing.T, got, want Stats) {
	t.Helper()
	if got != want {
		t.Fatalf("Stats = %v; want %v", got, want)
	}
	if got.In != got.Out+got.Shadowed+got.Folded+got.TombDropped {
		t.Fatalf("Stats 不守恒: %v", got)
	}
}

// 例一：Put(a,10)=1、Merge(a,5)=2、Merge(a,7)=3、Snapshot()=3、Delete(a)=4、Merge(a,1)=5。
func TestExampleOne(t *testing.T) {
	c := New(nil)
	if seq := mustPut(t, c, "a", 10); seq != 1 {
		t.Fatalf("seq = %d; want 1", seq)
	}
	mustMerge(t, c, "a", 5)
	mustMerge(t, c, "a", 7)
	snap := c.Snapshot()
	if snap != 3 {
		t.Fatalf("Snapshot() = %d; want 3", snap)
	}
	mustDelete(t, c, "a")
	mustMerge(t, c, "a", 1)

	wantGet(t, c, "a", 3, 22, true)
	wantGet(t, c, "a", Latest, 1, true)

	st := c.Compact()
	wantStats(t, st, Stats{In: 5, Out: 2, Folded: 3})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 3, Kind: KindPut, Value: 22},
		{Key: "a", Seq: 5, Kind: KindPut, Value: 1},
	})
	wantGet(t, c, "a", 3, 22, true)
	wantGet(t, c, "a", Latest, 1, true)

	t.Logf("输入: Put(a,10)@1 Merge(a,5)@2 Merge(a,7)@3 Snapshot=3 Delete(a)@4 Merge(a,1)@5")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: 最新条带 e1=Merge@5 并入 b=Delete@4 得 Put(1)@5 (Folded 1)；" +
		"快照3条带 7+5 并入 b=Put(10)@1 得 Put(22)@3 (Folded 2)；压实前后 Get(a,3)=22、Get(a,Latest)=1 不变")
}

// 例二（deeper 不含 b）：Put(b,4)=6、Delete(b)=7 同属最新条带，Delete 留下、
// Put 计 Shadowed；deeper 不含 b 时 Delete 被清除，b 全部消失。
func TestExampleTwoNoDeeper(t *testing.T) {
	c := New(nil)
	mustPut(t, c, "a", 10)
	mustMerge(t, c, "a", 5)
	mustMerge(t, c, "a", 7)
	c.Snapshot() // 3
	mustDelete(t, c, "a")
	mustMerge(t, c, "a", 1)
	mustPut(t, c, "b", 4)
	mustDelete(t, c, "b")

	st := c.Compact()
	wantStats(t, st, Stats{In: 7, Out: 2, Shadowed: 1, Folded: 3, TombDropped: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 3, Kind: KindPut, Value: 22},
		{Key: "a", Seq: 5, Kind: KindPut, Value: 1},
	})
	wantGet(t, c, "b", Latest, 0, false)

	t.Logf("输入: 例一 + Put(b,4)@6 Delete(b)@7, deeper 为空")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: b 的最新条带 e1=Delete@7 原样产出、Put@6 计 Shadowed；" +
		"deeper 不含 b，最旧 Delete 被清除 (TombDropped 1)，b 全部消失")
}

// 例二（deeper 含 b:9）：Delete 保留且 Get(b,Latest) 仍为不存在。
func TestExampleTwoWithDeeper(t *testing.T) {
	c := New(map[string]int64{"b": 9})
	mustPut(t, c, "a", 10)
	mustMerge(t, c, "a", 5)
	mustMerge(t, c, "a", 7)
	c.Snapshot() // 3
	mustDelete(t, c, "a")
	mustMerge(t, c, "a", 1)
	mustPut(t, c, "b", 4)
	mustDelete(t, c, "b")

	st := c.Compact()
	wantStats(t, st, Stats{In: 7, Out: 3, Shadowed: 1, Folded: 3})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 3, Kind: KindPut, Value: 22},
		{Key: "a", Seq: 5, Kind: KindPut, Value: 1},
		{Key: "b", Seq: 7, Kind: KindDelete, Value: 0},
	})
	wantGet(t, c, "b", Latest, 0, false)

	t.Logf("输入: 例一 + Put(b,4)@6 Delete(b)@7, deeper={b:9}")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: deeper 含 b，最旧 Delete 保留 (TombDropped 0)，Get(b,Latest) 仍为不存在")
}

// 快照号恰等于记录序号时，该记录属于该快照的条带（而非最新条带）。
func TestStripeBoundarySnapshotEqualsRecordSeq(t *testing.T) {
	c := New(nil)
	mustPut(t, c, "a", 1)   // @1
	snap := c.Snapshot()    // 1，恰等于记录 @1 的序号
	mustMerge(t, c, "a", 2) // @2

	st := c.Compact()
	// 若 @1 被错划到最新条带，会与 @2 折叠成 Put(3)@2（Out=1）；
	// 正确归属下 @1 属快照1条带原样产出，@2 属最新条带无基值留作 Merge。
	wantStats(t, st, Stats{In: 2, Out: 2})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 1, Kind: KindPut, Value: 1},
		{Key: "a", Seq: 2, Kind: KindMerge, Value: 2},
	})
	wantGet(t, c, "a", snap, 1, true)
	wantGet(t, c, "a", Latest, 3, true)

	t.Logf("输入: Put(a,1)@1 Snapshot=1 Merge(a,2)@2")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: stripe(q) 取不小于 q 的最小快照，q=1 恰等于快照号 1，故 @1 属快照1条带")
}

// 同一序号被多次持有只算一个条带；按次数释放，释放干净后才不再持有。
func TestDuplicateSnapshotHolds(t *testing.T) {
	c := New(nil)
	mustPut(t, c, "a", 1) // @1
	s1 := c.Snapshot()    // 1
	s2 := c.Snapshot()    // 1，第二次持有同一序号
	if s1 != s2 {
		t.Fatalf("s1=%d s2=%d; want equal", s1, s2)
	}
	if err := c.Release(s1); err != nil {
		t.Fatalf("Release(%d): %v", s1, err)
	}
	// 仍持有一次，Get 可用，且 Compact 只看到 S={1} 一个条带边界。
	wantGet(t, c, "a", 1, 1, true)
	mustMerge(t, c, "a", 2) // @2
	mustMerge(t, c, "a", 3) // @3

	st := c.Compact()
	wantStats(t, st, Stats{In: 3, Out: 2, Folded: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 1, Kind: KindPut, Value: 1},
		{Key: "a", Seq: 3, Kind: KindMerge, Value: 5},
	})

	if err := c.Release(s2); err != nil {
		t.Fatalf("Release(%d): %v", s2, err)
	}
	if _, _, err := c.Get("a", 1); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Get after double release: %v; want ErrNotHeld", err)
	}
	if err := c.Release(1); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Release after double release: %v; want ErrNotHeld", err)
	}

	t.Logf("输入: Put(a,1)@1 Snapshot=1 Snapshot=1 Release(1) Merge(a,2)@2 Merge(a,3)@3")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: S 为被持有序号的去重集合，{1,1} 去重为 {1}，@2 与 @3 同属最新条带折叠为 Merge(5)@3")
}

// Release 后相邻条带在下次 Compact 时合并；上次产出的 Merge 再被折叠。
func TestReleaseMergesStripesOnNextCompact(t *testing.T) {
	c := New(nil)
	mustPut(t, c, "a", 1)   // @1
	s1 := c.Snapshot()      // 1
	mustMerge(t, c, "a", 2) // @2
	s2 := c.Snapshot()      // 2
	mustMerge(t, c, "a", 4) // @3

	st1 := c.Compact()
	wantStats(t, st1, Stats{In: 3, Out: 3})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 1, Kind: KindPut, Value: 1},
		{Key: "a", Seq: 2, Kind: KindMerge, Value: 2},
		{Key: "a", Seq: 3, Kind: KindMerge, Value: 4},
	})
	wantGet(t, c, "a", s1, 1, true)
	wantGet(t, c, "a", s2, 3, true)
	wantGet(t, c, "a", Latest, 7, true)

	// 释放快照 2 后，条带 {2} 与最新条带相邻合并：Merge@2 与 Merge@3 折叠。
	if err := c.Release(s2); err != nil {
		t.Fatalf("Release(%d): %v", s2, err)
	}
	st2 := c.Compact()
	wantStats(t, st2, Stats{In: 3, Out: 2, Folded: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 1, Kind: KindPut, Value: 1},
		{Key: "a", Seq: 3, Kind: KindMerge, Value: 6},
	})
	wantGet(t, c, "a", s1, 1, true)
	wantGet(t, c, "a", Latest, 7, true)

	t.Logf("输入: Put(a,1)@1 Snapshot=1 Merge(a,2)@2 Snapshot=2 Merge(a,4)@3 Compact Release(2) Compact")
	t.Logf("输出: 第一次 stats=%v；第二次 records=%v stats=%v", st1, c.Records(), st2)
	t.Logf("判定依据: Release(2) 后 S={1}，@2、@3 同属最新条带，连续 Merge 折叠为 Merge(2+4)@3 (Folded 1)")
}

// Delete 后再 Merge：e1=Merge 并入 b=Delete 产出 Put(累计和)。
func TestDeleteThenMerge(t *testing.T) {
	c := New(nil)
	mustDelete(t, c, "a")   // @1
	mustMerge(t, c, "a", 5) // @2

	wantGet(t, c, "a", Latest, 5, true) // Delete 前（更新方向）累计过 Merge → 存在

	st := c.Compact()
	wantStats(t, st, Stats{In: 2, Out: 1, Folded: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 2, Kind: KindPut, Value: 5},
	})
	wantGet(t, c, "a", Latest, 5, true)

	t.Logf("输入: Delete(a)@1 Merge(a,5)@2")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: e1=Merge@2 向下遇 b=Delete@1，产出 Put(累计和 5)@2，j=2 计 Folded 1")
}

// 条带内 Merge 无基值时留作 Merge（deeper 含键，基值可为 0），
// 且上次产出的 Merge 在下次 Compact 中再被折叠。
func TestMergeWithoutBaseStaysMergeAndRefolds(t *testing.T) {
	c := New(map[string]int64{"a": 0}) // 基值可为 0，存在即算有
	mustMerge(t, c, "a", 3)            // @1
	mustMerge(t, c, "a", 4)            // @2

	st1 := c.Compact()
	wantStats(t, st1, Stats{In: 2, Out: 1, Folded: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 2, Kind: KindMerge, Value: 7},
	})
	wantGet(t, c, "a", Latest, 7, true)

	mustMerge(t, c, "a", 1) // @3
	st2 := c.Compact()
	// 上次产出的 Merge@2 与新 Merge@3 同属最新条带，再折叠为 Merge(8)@3。
	wantStats(t, st2, Stats{In: 2, Out: 1, Folded: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 3, Kind: KindMerge, Value: 8},
	})
	wantGet(t, c, "a", Latest, 8, true)

	t.Logf("输入: deeper={a:0}; Merge(a,3)@1 Merge(a,4)@2 Compact Merge(a,1)@3 Compact")
	t.Logf("输出: 第一次 stats=%v；第二次 records=%v stats=%v", st1, c.Records(), st2)
	t.Logf("判定依据: 条带耗尽无 b 产出 Merge(累计和)；deeper 含 a 故最旧 Merge 不转 Put；" +
		"下次 Compact 在上次产出与新记录上再运行")
}

// deeper 不含键时，最旧 Merge 改为同序号的 Put（计 MergeToPut）。
func TestMergeToPutWithoutDeeper(t *testing.T) {
	c := New(nil)
	mustMerge(t, c, "a", 3) // @1
	mustMerge(t, c, "a", 4) // @2

	st := c.Compact()
	wantStats(t, st, Stats{In: 2, Out: 1, Folded: 1, MergeToPut: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 2, Kind: KindPut, Value: 7},
	})
	wantGet(t, c, "a", Latest, 7, true)

	t.Logf("输入: Merge(a,3)@1 Merge(a,4)@2, deeper 为空")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: 收尾时最旧一条是 Merge 且 deeper 不含 a，改为同序号 Put(7)@2 (MergeToPut 1)")
}

// 连续多个条带的 Delete 被依次清除后，最旧 Merge 转 Put。
func TestTombstoneChainThenMergeToPut(t *testing.T) {
	c := New(nil)
	mustDelete(t, c, "a")   // @1
	s1 := c.Snapshot()      // 1
	mustDelete(t, c, "a")   // @2
	s2 := c.Snapshot()      // 2
	mustMerge(t, c, "a", 5) // @3

	st := c.Compact()
	// 产出（新到旧）: Merge(5)@3、Delete@2、Delete@1；
	// 收尾依次清除 Delete@1、Delete@2 (TombDropped 2)，最旧 Merge@3 转 Put (MergeToPut 1)。
	wantStats(t, st, Stats{In: 3, Out: 1, TombDropped: 2, MergeToPut: 1})
	wantRecords(t, c, []Record{
		{Key: "a", Seq: 3, Kind: KindPut, Value: 5},
	})
	wantGet(t, c, "a", Latest, 5, true)
	wantGet(t, c, "a", s1, 0, false)
	wantGet(t, c, "a", s2, 0, false)

	t.Logf("输入: Delete(a)@1 Snapshot=1 Delete(a)@2 Snapshot=2 Merge(a,5)@3")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: 最旧 Delete 反复清除直到不是 Delete；随后最旧 Merge 且 deeper 不含 a，转 Put")
}

// deeper 含键时：墓碑不清除、最旧 Merge 不转换。
func TestDeeperKeepsTombstoneAndMerge(t *testing.T) {
	c := New(map[string]int64{"b": 9, "m": 9})
	mustPut(t, c, "b", 4)   // @1
	mustDelete(t, c, "b")   // @2
	mustMerge(t, c, "m", 3) // @3

	st := c.Compact()
	wantStats(t, st, Stats{In: 3, Out: 2, Shadowed: 1})
	wantRecords(t, c, []Record{
		{Key: "b", Seq: 2, Kind: KindDelete, Value: 0},
		{Key: "m", Seq: 3, Kind: KindMerge, Value: 3},
	})
	wantGet(t, c, "b", Latest, 0, false)
	wantGet(t, c, "m", Latest, 12, true)

	t.Logf("输入: deeper={b:9,m:9}; Put(b,4)@1 Delete(b)@2 Merge(m,3)@3")
	t.Logf("输出: records=%v stats=%v", c.Records(), st)
	t.Logf("判定依据: deeper 含 b/m，最旧 Delete 不清除 (TombDropped 0)、最旧 Merge 不转 Put (MergeToPut 0)")
}

// 错误校验：空键、越界、未持有；校验顺序与被拒绝操作不消耗序号。
func TestErrors(t *testing.T) {
	c := New(nil)

	// 空键（写入先查空键再查越界）。
	if _, err := c.Put("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Put empty key: %v", err)
	}
	if _, err := c.Merge("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Merge empty key: %v", err)
	}
	if _, err := c.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Delete empty key: %v", err)
	}
	if _, err := c.Put("", maxAbsValue+1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Put empty key + out of range: %v; want ErrEmptyKey", err)
	}
	// Get 先查空键（即使序号也未持有）。
	if _, _, err := c.Get("", 12345); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Get empty key: %v; want ErrEmptyKey", err)
	}
	// 越界。
	if _, err := c.Put("a", maxAbsValue+1); !errors.Is(err, ErrRange) {
		t.Fatalf("Put out of range: %v", err)
	}
	if _, err := c.Put("a", -maxAbsValue-1); !errors.Is(err, ErrRange) {
		t.Fatalf("Put out of range (neg): %v", err)
	}
	if _, err := c.Merge("a", maxAbsValue+1); !errors.Is(err, ErrRange) {
		t.Fatalf("Merge out of range: %v", err)
	}
	// 未持有。
	if _, _, err := c.Get("a", 7); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Get not held: %v", err)
	}
	if err := c.Release(7); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Release not held: %v", err)
	}
	if err := c.Release(Latest); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Release(Latest): %v", err)
	}

	// 以上全部被拒绝，不得消耗序号：第一个成功写入序号必须为 1。
	if seq := mustPut(t, c, "a", 1); seq != 1 {
		t.Fatalf("seq = %d; want 1（被拒绝的操作不得消耗序号）", seq)
	}
	// 边界值 ±10^12 合法。
	mustPut(t, c, "b", maxAbsValue)
	mustPut(t, c, "c", -maxAbsValue)
	mustMerge(t, c, "b", maxAbsValue)
	mustMerge(t, c, "b", -maxAbsValue)

	snap := c.Snapshot()
	if err := c.Release(snap); err != nil {
		t.Fatalf("Release(%d): %v", snap, err)
	}
	if _, _, err := c.Get("a", snap); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Get after release: %v; want ErrNotHeld", err)
	}

	t.Logf("判定依据: 空键 ErrEmptyKey、越界 ErrRange、未持有 ErrNotHeld 可区分；" +
		"写入先查空键再查越界，Get 先查空键；被拒绝的操作不改变状态、不消耗序号")
}

// Get 语义：deeper 基值、Merge 累计、Put 短路、Delete 停止条件。
func TestGetSemantics(t *testing.T) {
	c := New(map[string]int64{"x": 5})
	wantGet(t, c, "x", Latest, 5, true)  // 仅 deeper
	wantGet(t, c, "y", Latest, 0, false) // 无任何记录

	mustMerge(t, c, "x", 2)             // @1
	wantGet(t, c, "x", Latest, 7, true) // Merge 累计 + deeper

	mustPut(t, c, "x", 100)               // @2
	wantGet(t, c, "x", Latest, 100, true) // Put 短路，不再加 deeper

	mustDelete(t, c, "x")                // @3
	wantGet(t, c, "x", Latest, 0, false) // Delete 停止且此前无 Merge

	mustMerge(t, c, "x", 4)             // @4
	wantGet(t, c, "x", Latest, 4, true) // Delete 前累计过 Merge → 存在

	mustPut(t, c, "k", 10)               // @5
	mustMerge(t, c, "k", 5)              // @6
	wantGet(t, c, "k", Latest, 15, true) // Merge 累计后遇 Put 加其值

	mustMerge(t, c, "z", 3)              // @7
	mustDelete(t, c, "z")                // @8
	wantGet(t, c, "z", Latest, 0, false) // Delete 遮蔽更旧的 Merge

	t.Logf("判定依据: 遇 Put 累计其值并停止；遇 Delete 停止（存在当且仅当此前累计过 Merge）；")
	t.Logf("判定依据: 到最旧未遇 Put/Delete 时存在当且仅当累计过 Merge 或 deeper 含键，并加上 deeper[k]")
}

// 尚无写入时 Snapshot 返回 0 且可持有、可用于 Get。
func TestSnapshotZero(t *testing.T) {
	c := New(nil)
	s := c.Snapshot()
	if s != 0 {
		t.Fatalf("Snapshot() = %d; want 0", s)
	}
	wantGet(t, c, "a", s, 0, false)
	st := c.Compact()
	wantStats(t, st, Stats{})
	mustPut(t, c, "a", 1)
	wantGet(t, c, "a", s, 0, false) // 快照 0 看不到 @1
	wantGet(t, c, "a", Latest, 1, true)
}

// Records 返回副本，外部修改不影响内部状态。
func TestRecordsCopyIsolation(t *testing.T) {
	c := New(nil)
	mustPut(t, c, "a", 1)
	recs := c.Records()
	recs[0].Value = 999
	recs[0].Key = "hacked"
	wantRecords(t, c, []Record{{Key: "a", Seq: 1, Kind: KindPut, Value: 1}})
	wantGet(t, c, "a", Latest, 1, true)
}

// 条带二分定位的探测次数不得超过 In*(floor(log2(|S|+1))+1)。
func TestStripeProbesBound(t *testing.T) {
	c := New(nil)
	for i := 0; i < 50; i++ {
		mustMerge(t, c, "a", 1)
		mustPut(t, c, "b", int64(i))
		if i%3 == 0 {
			c.Snapshot()
		}
	}
	st := c.Compact()
	snaps := len(c.holds)
	bound := st.In * (floorLog2(int64(snaps)+1) + 1)
	if c.stripeProbes > bound {
		t.Fatalf("stripeProbes = %d; 上限 In*(floor(log2(|S|+1))+1) = %d (In=%d, |S|=%d)",
			c.stripeProbes, bound, st.In, snaps)
	}
	t.Logf("输入: 50 组 (Merge,Put) 与 17 个快照；输出: stats=%v", st)
	t.Logf("判定依据: stripeProbes=%d <= In*(floor(log2(|S|+1))+1)=%d", c.stripeProbes, bound)
}

func floorLog2(n int64) int64 {
	var r int64
	for n > 1 {
		n >>= 1
		r++
	}
	return r
}
