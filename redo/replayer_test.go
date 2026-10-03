package redo

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, w, m int) *Replayer {
	t.Helper()
	r, err := NewReplayer(w, m)
	if err != nil {
		t.Fatalf("NewReplayer(%d,%d) 失败: %v", w, m, err)
	}
	return r
}

func mustFeed(t *testing.T, r *Replayer, rec Record) {
	t.Helper()
	if err := r.Feed(rec); err != nil {
		t.Fatalf("Feed(%+v) 被意外拒绝: %v", rec, err)
	}
}

func mustFinish(t *testing.T, r *Replayer) {
	t.Helper()
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 被意外拒绝: %v", err)
	}
}

func requireErrCode(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望拒绝原因 %s，但操作被接受", code)
	}
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("错误类型不是 *Error: %v", err)
	}
	if re.Code != code {
		t.Fatalf("期望拒绝原因 %s，实际 %s (%v)", code, re.Code, err)
	}
}

func requirePage(t *testing.T, r *Replayer, space, page int, wantValue, wantLSN int64) {
	t.Helper()
	v, l := r.PageState(space, page)
	if v != wantValue || l != wantLSN {
		t.Fatalf("页 (%d,%d) = (%d,%d)，期望 (%d,%d)", space, page, v, l, wantValue, wantLSN)
	}
}

func requireStats(t *testing.T, r *Replayer, want Stats) {
	t.Helper()
	if got := r.Stats(); got != want {
		t.Fatalf("Stats=%+v，期望 %+v", got, want)
	}
}

var (
	_ = reflect.DeepEqual
	_ = sync.Mutex{}
)

// 规格中的完整示例。
func TestSpecExample(t *testing.T) {
	r := mustNew(t, 2, 3)
	if err := r.LoadPage(1, 0, 100, 5); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(4, 1, 1, 0, 1))
	mustFeed(t, r, PageRecord(5, 1, 1, 1, 2))
	mustFeed(t, r, EndRecord(6, 1))
	if got := r.Stats(); got.Pending != 2 || got.Batches != 0 {
		t.Fatalf("pending=2 小于 M=3 不应批次, stats=%+v", got)
	}
	mustFeed(t, r, PageRecord(7, 2, 1, 0, 10))
	mustFeed(t, r, EndRecord(8, 2))
	if got := r.Stats(); got.Batches != 1 || got.Pending != 0 {
		t.Fatalf("pending=3 不小于 M=3 应立即批次, stats=%+v", got)
	}
	want := []ApplyEntry{{LSN: 5, Result: Applied}, {LSN: 4, Result: Skipped}, {LSN: 7, Result: Applied}}
	if got := r.ApplyLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyLog=%v，期望 %v", got, want)
	}
	requirePage(t, r, 1, 0, 110, 7)
	requirePage(t, r, 1, 1, 2, 5)
}

// lsn 恰等于 pageLSN 跳过，大 1 应用。
func TestSkipEqualAndApplyPlusOne(t *testing.T) {
	r := mustNew(t, 1, 10)
	if err := r.LoadPage(1, 0, 100, 5); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	if err := r.LoadPage(1, 1, 0, 5); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(5, 1, 1, 0, 1)) // lsn 恰等于 pageLSN=5，应跳过
	mustFeed(t, r, PageRecord(6, 1, 1, 1, 1)) // lsn 比 pageLSN=5 大 1，应应用
	mustFeed(t, r, EndRecord(7, 1))
	mustFinish(t, r)
	want := []ApplyEntry{{LSN: 5, Result: Skipped}, {LSN: 6, Result: Applied}}
	if got := r.ApplyLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyLog=%v，期望 %v", got, want)
	}
	requirePage(t, r, 1, 0, 100, 5)
	requirePage(t, r, 1, 1, 1, 6)
	requireStats(t, r, Stats{Batches: 1, Accepted: 2, Applied: 1, Skipped: 1})
}

// End 之前的记录只暂存，不入队也不计 pending。
func TestStagedNotPendingBeforeEnd(t *testing.T) {
	r := mustNew(t, 2, 2)
	mustFeed(t, r, PageRecord(1, 1, 1, 0, 1))
	mustFeed(t, r, PageRecord(2, 1, 1, 1, 1))
	if got := r.Stats(); got.Staged != 2 || got.Pending != 0 || got.Batches != 0 {
		t.Fatalf("End 前不应入队计 pending, stats=%+v", got)
	}
	if got := r.ApplyLog(); len(got) != 0 {
		t.Fatalf("End 前 ApplyLog 应为空, got=%v", got)
	}
	mustFeed(t, r, EndRecord(3, 1)) // pending=2 恰等于 M=2，立即批次
	if got := r.Stats(); got.Pending != 0 || got.Staged != 0 || got.Batches != 1 {
		t.Fatalf("End 后应立即批次, stats=%+v", got)
	}
}

// 批次触发恰在 pending 等于 M 时，且一个 End 可一次越过 M（只算一个批次）。
func TestBatchTriggerExactlyAtMAndOvershoot(t *testing.T) {
	// 恰在 pending == M 触发。
	r := mustNew(t, 1, 3)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(1, 1, 1, 0, 1))
	mustFeed(t, r, EndRecord(2, 1))
	mustFeed(t, r, PageRecord(3, 2, 1, 0, 1))
	mustFeed(t, r, EndRecord(4, 2))
	if got := r.Stats(); got.Pending != 2 || got.Batches != 0 {
		t.Fatalf("pending=2 小于 M=3 不应批次, stats=%+v", got)
	}
	mustFeed(t, r, PageRecord(5, 3, 1, 0, 1))
	mustFeed(t, r, EndRecord(6, 3))
	if got := r.Stats(); got.Batches != 1 || got.Pending != 0 {
		t.Fatalf("pending=3 恰等于 M=3 应立即批次, stats=%+v", got)
	}

	// 一个 End 使 pending 从 0 越过 M=2 到 3，只做一个批次且全部应用。
	r2 := mustNew(t, 1, 2)
	if err := r2.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r2, PageRecord(1, 1, 1, 0, 1))
	mustFeed(t, r2, PageRecord(2, 1, 1, 0, 1))
	mustFeed(t, r2, PageRecord(3, 1, 1, 0, 1))
	mustFeed(t, r2, EndRecord(4, 1))
	requireStats(t, r2, Stats{Batches: 1, Accepted: 3, Applied: 3})
	requirePage(t, r2, 1, 0, 3, 3)
}

// 批次内跨线程处理次序按线程号 0..W-1，而非 LSN。
func TestBatchOrderByThreadNumberNotLSN(t *testing.T) {
	r := mustNew(t, 2, 100)
	// 页 (1,0) 的线程为 (7+0)%2=1，页 (1,1) 的线程为 (7+1)%2=0。
	mustFeed(t, r, FileRecord(1, 1, Create, 1))
	mustFeed(t, r, PageRecord(2, 2, 1, 0, 1)) // lsn=2 进线程 1
	mustFeed(t, r, EndRecord(3, 2))
	mustFeed(t, r, PageRecord(4, 3, 1, 1, 1)) // lsn=4 进线程 0
	mustFeed(t, r, EndRecord(5, 3))
	mustFinish(t, r)
	want := []ApplyEntry{{LSN: 4, Result: Applied}, {LSN: 2, Result: Applied}}
	if got := r.ApplyLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("批次内应按线程号次序处理，ApplyLog=%v，期望 %v", got, want)
	}
}

// File 到达时 pending 为 0 不算批次；pending 大于 0 先做批次屏障再执行文件操作。
func TestFileBarrierAndZeroPendingNoBatch(t *testing.T) {
	r := mustNew(t, 1, 100)
	mustFeed(t, r, FileRecord(1, 1, Create, 1))
	if got := r.Stats(); got.Batches != 0 {
		t.Fatalf("pending=0 时 File 不应产生批次, stats=%+v", got)
	}
	mustFeed(t, r, PageRecord(2, 2, 1, 0, 5))
	mustFeed(t, r, EndRecord(3, 2))             // pending=1 < M=100，不批次
	mustFeed(t, r, FileRecord(4, 3, Create, 2)) // pending=1 > 0，先批次再 Create
	if got := r.Stats(); got.Batches != 1 {
		t.Fatalf("File 到达时 pending>0 应先做一个批次, stats=%+v", got)
	}
	requirePage(t, r, 1, 0, 5, 2)
	mustFeed(t, r, FileRecord(5, 4, Delete, 9)) // 不存在的空间且 pending=0：无批次无事发生
	if got := r.Stats(); got.Batches != 1 {
		t.Fatalf("pending=0 时 File 不应产生批次, stats=%+v", got)
	}
}

// Delete 后同空间的 Page 记录被丢弃（空间缺失）。
func TestPageDroppedAfterDelete(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustFeed(t, r, FileRecord(1, 1, Create, 1))
	mustFeed(t, r, PageRecord(2, 2, 1, 0, 5))
	mustFeed(t, r, EndRecord(3, 2))
	requirePage(t, r, 1, 0, 5, 2)
	mustFeed(t, r, FileRecord(4, 3, Delete, 1))
	mustFeed(t, r, PageRecord(5, 4, 1, 0, 7))
	mustFeed(t, r, EndRecord(6, 4))
	requireStats(t, r, Stats{Batches: 2, Accepted: 2, Applied: 1, Missing: 1})
	want := []ApplyEntry{{LSN: 2, Result: Applied}, {LSN: 5, Result: SpaceMissing}}
	if got := r.ApplyLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyLog=%v，期望 %v", got, want)
	}
	requirePage(t, r, 1, 0, 0, 0)
}

// Delete 再 Create 后，空间里的页回到 value=0、pageLSN=0。
func TestDeleteThenCreateResetsPages(t *testing.T) {
	r := mustNew(t, 1, 1)
	if err := r.LoadPage(1, 0, 100, 5); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, FileRecord(1, 1, Delete, 1))
	mustFeed(t, r, FileRecord(2, 2, Create, 1))
	requirePage(t, r, 1, 0, 0, 0)
	mustFeed(t, r, PageRecord(3, 3, 1, 0, 7))
	mustFeed(t, r, EndRecord(4, 3))
	requirePage(t, r, 1, 0, 7, 3)
}

// Create 已存在的空间不清页。
func TestCreateExistingKeepsPages(t *testing.T) {
	r := mustNew(t, 1, 1)
	if err := r.LoadPage(1, 0, 100, 5); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, FileRecord(1, 1, Create, 1))
	requirePage(t, r, 1, 0, 100, 5)
	mustFeed(t, r, PageRecord(6, 2, 1, 0, 1))
	mustFeed(t, r, EndRecord(7, 2))
	requirePage(t, r, 1, 0, 101, 6)
}

// Delete 不存在的空间无事发生。
func TestDeleteNonExistingNoop(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustFeed(t, r, FileRecord(1, 1, Delete, 5))
	requirePage(t, r, 5, 0, 0, 0)
	if got := r.Stats(); got.Batches != 0 {
		t.Fatalf("Delete 不存在的空间不应产生批次, stats=%+v", got)
	}
}

// Finish 丢弃未 End 的 mtr 暂存记录，其前已 End 的小事务仍被应用。
func TestFinishDiscardsOpenMTR(t *testing.T) {
	r := mustNew(t, 1, 100)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(1, 1, 1, 0, 1))
	mustFeed(t, r, EndRecord(2, 1))             // 已 End，进入队列
	mustFeed(t, r, PageRecord(3, 2, 1, 0, 100)) // 未 End，仅暂存
	mustFinish(t, r)
	requireStats(t, r, Stats{Batches: 1, Accepted: 2, Applied: 1, Discarded: 1})
	requirePage(t, r, 1, 0, 1, 1)
	want := []ApplyEntry{{LSN: 1, Result: Applied}}
	if got := r.ApplyLog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyLog=%v，期望 %v", got, want)
	}
}

// Load 在 Feed 之后被拒（回放已开始）。
func TestLoadAfterFeedRejected(t *testing.T) {
	r := mustNew(t, 1, 10)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("首次 LoadPage 失败: %v", err)
	}
	mustFeed(t, r, FileRecord(1, 1, Create, 3))
	requireErrCode(t, r.LoadPage(2, 0, 0, 0), ErrStarted)
	// 被拒绝的 Load 不改变状态：空间 2 仍不存在。
	mustFeed(t, r, PageRecord(2, 2, 2, 0, 1))
	mustFeed(t, r, EndRecord(3, 2))
	mustFinish(t, r)
	requireStats(t, r, Stats{Batches: 1, Accepted: 1, Missing: 1})
}

// mtr 号回退、File 夹在未结束 mtr 中、End 不匹配均被拒。
func TestMTRRegressionAndFileInsideOpenMTR(t *testing.T) {
	r := mustNew(t, 1, 10)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(1, 2, 1, 0, 1))
	mustFeed(t, r, EndRecord(2, 2))
	// 新开 mtr 的号回退到 1，不大于此前最大号 2。
	requireErrCode(t, r.Feed(PageRecord(3, 1, 1, 0, 1)), ErrMTR)
	// lsn=3 未被消耗，可用同一 lsn 重投合法记录。
	mustFeed(t, r, PageRecord(3, 3, 1, 0, 1))
	// File 夹在未结束的 mtr3 之中。
	requireErrCode(t, r.Feed(FileRecord(4, 4, Create, 1)), ErrMTR)
	// File 的 mtr 号 4 未推进最大号：用 mtr=4 开新 mtr 仍合法。
	mustFeed(t, r, EndRecord(4, 3))
	mustFeed(t, r, PageRecord(5, 4, 1, 0, 1))
	// End 的 mtr 与未结束的不一致。
	requireErrCode(t, r.Feed(EndRecord(6, 5)), ErrMTR)
	mustFeed(t, r, EndRecord(6, 4))
	// 没有未结束的 mtr 时 End 被拒。
	requireErrCode(t, r.Feed(EndRecord(7, 4)), ErrMTR)
	// Page 的 mtr 号与未结束的 mtr 不同。
	mustFeed(t, r, PageRecord(7, 5, 1, 0, 1))
	requireErrCode(t, r.Feed(PageRecord(8, 6, 1, 0, 1)), ErrMTR)
	mustFeed(t, r, PageRecord(8, 5, 1, 0, 1))
	mustFeed(t, r, EndRecord(9, 5))
	mustFinish(t, r)
	requireStats(t, r, Stats{Batches: 1, Accepted: 5, Applied: 5})
	requirePage(t, r, 1, 0, 5, 8)
}

// 被拒绝的记录不推进已接受的最大 LSN，同一 lsn 可重投。
func TestRejectedDoesNotAdvanceLSN(t *testing.T) {
	r := mustNew(t, 1, 10)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	mustFeed(t, r, PageRecord(10, 1, 1, 0, 1))
	requireErrCode(t, r.Feed(PageRecord(10, 1, 1, 0, 1)), ErrLSN) // 相等，不够大
	requireErrCode(t, r.Feed(PageRecord(5, 1, 1, 0, 1)), ErrLSN)  // 更小
	// mtr 错误也不消耗 lsn。
	requireErrCode(t, r.Feed(PageRecord(11, 2, 1, 0, 1)), ErrMTR)
	mustFeed(t, r, PageRecord(11, 1, 1, 0, 1)) // 同一 lsn=11 重投成功
	mustFeed(t, r, EndRecord(12, 1))
	mustFinish(t, r)
	requireStats(t, r, Stats{Batches: 1, Accepted: 2, Applied: 2})
	requirePage(t, r, 1, 0, 2, 11)
}

// Finish 之后 Feed、Load 与再次 Finish 都被拒（回放已结束）。
func TestFinishRejectsFurtherOps(t *testing.T) {
	r := mustNew(t, 1, 10)
	mustFinish(t, r)
	requireErrCode(t, r.Feed(PageRecord(1, 1, 1, 0, 1)), ErrFinished)
	requireErrCode(t, r.Feed(FileRecord(1, 1, Create, 1)), ErrFinished)
	requireErrCode(t, r.Feed(EndRecord(1, 1)), ErrFinished)
	requireErrCode(t, r.LoadPage(1, 0, 0, 0), ErrFinished)
	requireErrCode(t, r.Finish(), ErrFinished)
}

// 参数非法：构造参数、各号、lsn、mtr、kind、Load 的取值范围。
func TestInvalidArgs(t *testing.T) {
	for _, wm := range [][2]int{{0, 1}, {65, 1}, {-1, 1}, {1, 0}, {1, 1_000_001}, {1, -1}} {
		if _, err := NewReplayer(wm[0], wm[1]); err == nil {
			t.Fatalf("NewReplayer(%d,%d) 应报参数非法", wm[0], wm[1])
		} else {
			requireErrCode(t, err, ErrInvalidArgs)
		}
	}
	r := mustNew(t, 1, 10)
	requireErrCode(t, r.LoadPage(1_000_001, 0, 0, 0), ErrInvalidArgs)
	requireErrCode(t, r.LoadPage(-1, 0, 0, 0), ErrInvalidArgs)
	requireErrCode(t, r.LoadPage(0, 1_000_001, 0, 0), ErrInvalidArgs)
	requireErrCode(t, r.LoadPage(0, 0, 0, -1), ErrInvalidArgs)
	requireErrCode(t, r.LoadPage(0, 0, 0, MaxLSN+1), ErrInvalidArgs)
	if err := r.LoadPage(MaxSpacePage, MaxSpacePage, 0, MaxLSN); err != nil {
		t.Fatalf("边界取值应合法: %v", err)
	}
	requireErrCode(t, r.Feed(PageRecord(0, 1, 0, 0, 1)), ErrInvalidArgs)        // lsn < 1
	requireErrCode(t, r.Feed(PageRecord(MaxLSN+1, 1, 0, 0, 1)), ErrInvalidArgs) // lsn 越界
	requireErrCode(t, r.Feed(PageRecord(1, 0, 0, 0, 1)), ErrInvalidArgs)        // mtr < 1
	requireErrCode(t, r.Feed(PageRecord(1, 1, 1_000_001, 0, 1)), ErrInvalidArgs)
	requireErrCode(t, r.Feed(PageRecord(1, 1, 0, -1, 1)), ErrInvalidArgs)
	requireErrCode(t, r.Feed(FileRecord(1, 1, Kind(7), 0)), ErrInvalidArgs) // kind 非法
	requireErrCode(t, r.Feed(Record{Type: RecType(9), LSN: 1, MTR: 1}), ErrInvalidArgs)
	// 参数非法的记录不推进 LSN。
	mustFeed(t, r, PageRecord(1, 1, MaxSpacePage, 0, 1))
	mustFeed(t, r, EndRecord(2, 1))
	mustFinish(t, r)
	requireStats(t, r, Stats{Batches: 1, Accepted: 1, Applied: 1})
}

// 同一份日志在 W=1 与大 W、不同 M 下最终页状态与计数一致。
func TestW1AndLargeWConsistent(t *testing.T) {
	type key struct{ space, page int }
	type val struct{ value, lsn int64 }

	loads := [][4]int64{{1, 0, 10, 1}, {2, 0, 0, 0}, {1, 1, 0, 3}}
	recs := []Record{
		PageRecord(2, 1, 1, 0, 1),
		PageRecord(3, 1, 2, 0, 2),
		EndRecord(4, 1),
		PageRecord(5, 2, 1, 1, 1),
		EndRecord(6, 2),
		FileRecord(7, 3, Create, 3),
		PageRecord(8, 4, 3, 5, 9),
		EndRecord(9, 4),
		FileRecord(10, 5, Delete, 1),
		PageRecord(11, 6, 1, 0, 100), // Delete 后空间缺失
		EndRecord(12, 6),
		FileRecord(13, 7, Create, 1),
		PageRecord(14, 8, 1, 0, 7),
		PageRecord(15, 8, 3, 5, 1),
		EndRecord(16, 8),
		PageRecord(17, 9, 2, 0, 1), // 未 End，被丢弃
	}
	keys := []key{{1, 0}, {1, 1}, {2, 0}, {3, 5}}

	run := func(w, m int) (map[key]val, Stats) {
		r, err := NewReplayer(w, m)
		if err != nil {
			t.Fatalf("NewReplayer(%d,%d) 失败: %v", w, m, err)
		}
		for _, l := range loads {
			if err := r.LoadPage(int(l[0]), int(l[1]), l[2], l[3]); err != nil {
				t.Fatalf("LoadPage 失败: %v", err)
			}
		}
		for _, rec := range recs {
			if err := r.Feed(rec); err != nil {
				t.Fatalf("Feed(%+v) 失败: %v", rec, err)
			}
		}
		if err := r.Finish(); err != nil {
			t.Fatalf("Finish 失败: %v", err)
		}
		states := make(map[key]val)
		for _, k := range keys {
			v, l := r.PageState(k.space, k.page)
			states[k] = val{v, l}
		}
		return states, r.Stats()
	}

	base, baseStats := run(1, 3)
	for _, wm := range [][2]int{{64, 1}, {7, 1000}, {2, 3}, {64, 1_000_000}} {
		states, stats := run(wm[0], wm[1])
		if !reflect.DeepEqual(states, base) {
			t.Fatalf("W=%d,M=%d 最终页状态 %v 与 W=1,M=3 的 %v 不一致", wm[0], wm[1], states, base)
		}
		if stats.Accepted != baseStats.Accepted || stats.Applied != baseStats.Applied ||
			stats.Skipped != baseStats.Skipped || stats.Missing != baseStats.Missing ||
			stats.Discarded != baseStats.Discarded {
			t.Fatalf("W=%d,M=%d 计数 %+v 与 W=1,M=3 的 %+v 不一致", wm[0], wm[1], stats, baseStats)
		}
	}
	// 不变量：已接受 Page 记录数 = 应用+已应用+空间缺失+被丢弃（结束后无暂存与排队）。
	if baseStats.Accepted != baseStats.Applied+baseStats.Skipped+baseStats.Missing+baseStats.Discarded {
		t.Fatalf("不变量被破坏: %+v", baseStats)
	}
	if baseStats.Pending != 0 || baseStats.Staged != 0 {
		t.Fatalf("结束后不应有暂存或排队记录: %+v", baseStats)
	}
	t.Logf("输入 loads=%v recs=%v", loads, recs)
	t.Logf("输出 states=%v stats=%+v（判定依据：与 W=1,M=3 顺序回放一致）", base, baseStats)
}

// 并发调用所有操作与查询，结果等价于某个串行顺序（配合 -race 验证）。
func TestConcurrentAccess(t *testing.T) {
	r := mustNew(t, 4, 5)
	if err := r.LoadPage(1, 0, 0, 0); err != nil {
		t.Fatalf("LoadPage 失败: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = r.PageState(1, 0)
				_ = r.ApplyLog()
				_ = r.Stats()
			}
		}()
	}
	lsn := int64(1)
	for mtr := 1; mtr <= 20; mtr++ {
		if err := r.Feed(PageRecord(lsn, mtr, 1, 0, 1)); err != nil {
			t.Fatalf("Feed 失败: %v", err)
		}
		lsn++
		if err := r.Feed(EndRecord(lsn, mtr)); err != nil {
			t.Fatalf("Feed 失败: %v", err)
		}
		lsn++
	}
	wg.Wait()
	mustFinish(t, r)
	requirePage(t, r, 1, 0, 20, 39)
	requireStats(t, r, Stats{Batches: 4, Accepted: 20, Applied: 20})
}
