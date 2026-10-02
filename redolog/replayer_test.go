package redolog

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, w, m int) *Replayer {
	t.Helper()
	r, err := NewReplayer(w, m)
	if err != nil {
		t.Fatalf("NewReplayer(%d, %d) 失败: %v", w, m, err)
	}
	return r
}

func mustLoad(t *testing.T, r *Replayer, space, page, value, lsn int64) {
	t.Helper()
	if err := r.LoadPage(space, page, value, lsn); err != nil {
		t.Fatalf("LoadPage(%d,%d,%d,%d) 失败: %v", space, page, value, lsn, err)
	}
}

func mustFeed(t *testing.T, r *Replayer, rec Record) {
	t.Helper()
	if err := r.Feed(rec); err != nil {
		t.Fatalf("Feed(%+v) 失败: %v", rec, err)
	}
}

func feedErr(t *testing.T, r *Replayer, rec Record) ErrReason {
	t.Helper()
	err := r.Feed(rec)
	if err == nil {
		t.Fatalf("Feed(%+v) 应被拒绝", rec)
	}
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("Feed 错误类型不可区分: %v", err)
	}
	return rerr.Reason
}

func page(lsn, mtr, space, pg, delta int64) Record {
	return Record{Type: RecPage, LSN: lsn, Mtr: mtr, Space: space, Page: pg, Delta: delta}
}

func file(lsn, mtr int64, kind FileKind, space int64) Record {
	return Record{Type: RecFile, LSN: lsn, Mtr: mtr, Kind: kind, Space: space}
}

func end(lsn, mtr int64) Record {
	return Record{Type: RecEnd, LSN: lsn, Mtr: mtr}
}

func checkPage(t *testing.T, r *Replayer, space, pg, wantValue, wantLSN int64) {
	t.Helper()
	v, l, ok := r.PageState(space, pg)
	if !ok {
		t.Fatalf("表空间 %d 应存在", space)
	}
	if v != wantValue || l != wantLSN {
		t.Fatalf("页 (%d,%d) = (%d,%d)，期望 (%d,%d)", space, pg, v, l, wantValue, wantLSN)
	}
}

func checkNoSpace(t *testing.T, r *Replayer, space int64) {
	t.Helper()
	if _, _, ok := r.PageState(space, 0); ok {
		t.Fatalf("表空间 %d 应不存在", space)
	}
}

func checkApplyLog(t *testing.T, r *Replayer, want []ApplyEntry) {
	t.Helper()
	got := r.ApplyLog()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplyLog = %v，期望 %v", got, want)
	}
}

// 规格示例：W=2、M=3，批次内按线程号而非 LSN 处理。
func TestSpecExample(t *testing.T) {
	r := mustNew(t, 2, 3)
	mustLoad(t, r, 1, 0, 100, 5)
	mustFeed(t, r, page(4, 1, 1, 0, 1))
	mustFeed(t, r, page(5, 1, 1, 1, 2))
	mustFeed(t, r, end(6, 1))
	if got := r.Pending(); got != 2 {
		t.Fatalf("pending = %d，期望 2", got)
	}
	if got := r.Batches(); got != 0 {
		t.Fatalf("batches = %d，期望 0", got)
	}
	mustFeed(t, r, page(7, 2, 1, 0, 10))
	mustFeed(t, r, end(8, 2))
	if got := r.Batches(); got != 1 {
		t.Fatalf("batches = %d，期望 1", got)
	}
	if got := r.Pending(); got != 0 {
		t.Fatalf("pending = %d，期望 0", got)
	}
	checkApplyLog(t, r, []ApplyEntry{
		{LSN: 5, Result: Applied},
		{LSN: 4, Result: AlreadyApplied},
		{LSN: 7, Result: Applied},
	})
	checkPage(t, r, 1, 0, 110, 7)
	checkPage(t, r, 1, 1, 2, 5)
}

// lsn 恰等于 pageLSN 跳过，大 1 应用。
func TestLSNEqualSkipsAndPlusOneApplies(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 100, 5)
	mustFeed(t, r, page(5, 1, 1, 0, 7))
	mustFeed(t, r, end(6, 1))
	checkApplyLog(t, r, []ApplyEntry{{LSN: 5, Result: AlreadyApplied}})
	checkPage(t, r, 1, 0, 100, 5)

	mustFeed(t, r, page(7, 2, 1, 0, 7))
	mustFeed(t, r, end(8, 2))
	checkApplyLog(t, r, []ApplyEntry{
		{LSN: 5, Result: AlreadyApplied},
		{LSN: 7, Result: Applied},
	})
	checkPage(t, r, 1, 0, 107, 7)
}

// End 之前的记录不入队也不计 pending。
func TestNotEnqueuedBeforeEnd(t *testing.T) {
	r := mustNew(t, 2, 1)
	mustFeed(t, r, page(1, 1, 3, 0, 5))
	mustFeed(t, r, page(2, 1, 3, 1, 6))
	if got := r.Pending(); got != 0 {
		t.Fatalf("End 前 pending = %d，期望 0", got)
	}
	if got := r.Batches(); got != 0 {
		t.Fatalf("End 前 batches = %d，期望 0", got)
	}
	if got := len(r.ApplyLog()); got != 0 {
		t.Fatalf("End 前 ApplyLog 长度 = %d，期望 0", got)
	}
	st := r.Stats()
	if st.Buffered != 2 || st.AcceptedPages != 2 {
		t.Fatalf("End 前暂存统计错误: %+v", st)
	}
	mustFeed(t, r, end(3, 1))
	if got := r.Batches(); got != 1 {
		t.Fatalf("End 后 batches = %d，期望 1（M=1，pending=2>=1）", got)
	}
}

// 批次触发恰在 pending 等于 M 时，且一个 End 可一次越过 M。
func TestBatchTriggerExactlyAtM(t *testing.T) {
	r := mustNew(t, 1, 3)
	mustFeed(t, r, page(1, 1, 1, 0, 1))
	mustFeed(t, r, end(2, 1))
	if got := r.Batches(); got != 0 {
		t.Fatalf("pending=1 < M=3 不应批次，batches = %d", got)
	}
	mustFeed(t, r, page(3, 2, 1, 1, 1))
	mustFeed(t, r, end(4, 2))
	if got := r.Batches(); got != 0 {
		t.Fatalf("pending=2 < M=3 不应批次，batches = %d", got)
	}
	mustFeed(t, r, page(5, 3, 1, 2, 1))
	mustFeed(t, r, end(6, 3))
	if got := r.Batches(); got != 1 {
		t.Fatalf("pending=3 = M=3 应立即批次，batches = %d", got)
	}

	// 一个 End 带入 5 条记录，pending 从 0 直接越过 M=3，只算一个批次。
	r2 := mustNew(t, 1, 3)
	for i := int64(0); i < 5; i++ {
		mustFeed(t, r2, page(10+i, 1, 1, i, 1))
	}
	if got := r2.Pending(); got != 0 {
		t.Fatalf("End 前 pending = %d，期望 0", got)
	}
	mustFeed(t, r2, end(20, 1))
	if got := r2.Batches(); got != 1 {
		t.Fatalf("pending 一次越过 M 应只算一个批次，batches = %d", got)
	}
	if got := r2.Pending(); got != 0 {
		t.Fatalf("批次后 pending = %d，期望 0", got)
	}
	if got := len(r2.ApplyLog()); got != 5 {
		t.Fatalf("ApplyLog 长度 = %d，期望 5", got)
	}
}

// File 到达时 pending 为 0 不算批次；pending 大于 0 时先做一个批次再执行文件操作。
func TestFileBarrier(t *testing.T) {
	r := mustNew(t, 1, 100)
	mustFeed(t, r, file(1, 1, FileCreate, 5))
	if got := r.Batches(); got != 0 {
		t.Fatalf("pending 为 0 时 File 不应触发批次，batches = %d", got)
	}
	if _, _, ok := r.PageState(5, 0); !ok {
		t.Fatalf("Create 后表空间 5 应存在")
	}

	mustFeed(t, r, page(2, 2, 5, 0, 10))
	mustFeed(t, r, end(3, 2))
	if got := r.Pending(); got != 1 {
		t.Fatalf("pending = %d，期望 1", got)
	}
	mustFeed(t, r, file(4, 3, FileDelete, 5))
	if got := r.Batches(); got != 1 {
		t.Fatalf("File 屏障应先做一个批次，batches = %d", got)
	}
	checkApplyLog(t, r, []ApplyEntry{{LSN: 2, Result: Applied}})
	checkNoSpace(t, r, 5)
}

// Delete 后同空间的 Page 记录被丢弃（空间缺失）。
func TestPageAfterDeleteSpaceMissing(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 100, 5)
	mustFeed(t, r, file(1, 1, FileDelete, 1))
	checkNoSpace(t, r, 1)
	mustFeed(t, r, page(2, 2, 1, 0, 10))
	mustFeed(t, r, end(3, 2))
	checkApplyLog(t, r, []ApplyEntry{{LSN: 2, Result: SpaceMissing}})
	st := r.Stats()
	if st.SpaceMissing != 1 || st.Applied != 0 {
		t.Fatalf("统计错误: %+v", st)
	}
}

// Delete 再 Create 后页回到 value=0、pageLSN=0。
func TestDeleteThenCreateResetsPages(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 100, 5)
	mustFeed(t, r, page(6, 1, 1, 0, 10))
	mustFeed(t, r, end(7, 1))
	checkPage(t, r, 1, 0, 110, 6)
	mustFeed(t, r, file(8, 2, FileDelete, 1))
	mustFeed(t, r, file(9, 3, FileCreate, 1))
	checkPage(t, r, 1, 0, 0, 0)
	mustFeed(t, r, page(10, 4, 1, 0, 3))
	mustFeed(t, r, end(11, 4))
	checkPage(t, r, 1, 0, 3, 10)
}

// Create 已存在的空间不清页。
func TestCreateExistingKeepsPages(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 100, 5)
	mustFeed(t, r, file(1, 1, FileCreate, 1))
	checkPage(t, r, 1, 0, 100, 5)
}

// Delete 不存在的空间无事发生。
func TestDeleteNonexistentNoop(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustFeed(t, r, file(1, 1, FileDelete, 9))
	checkNoSpace(t, r, 9)
	if got := r.Batches(); got != 0 {
		t.Fatalf("batches = %d，期望 0", got)
	}
}

// Finish 丢弃未 End 的 mtr，其前已 End 的小事务仍被应用。
func TestFinishDiscardsUnendedMtr(t *testing.T) {
	r := mustNew(t, 1, 100)
	mustLoad(t, r, 1, 0, 0, 0)
	mustFeed(t, r, page(1, 1, 1, 0, 10))
	mustFeed(t, r, end(2, 1))
	mustFeed(t, r, page(3, 2, 1, 0, 100))
	mustFeed(t, r, page(4, 2, 1, 1, 200))
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	if got := r.Batches(); got != 1 {
		t.Fatalf("Finish 应将 pending=1 做一个批次，batches = %d", got)
	}
	checkApplyLog(t, r, []ApplyEntry{{LSN: 1, Result: Applied}})
	checkPage(t, r, 1, 0, 10, 1)
	checkPage(t, r, 1, 1, 0, 0)
	st := r.Stats()
	if st.Discarded != 2 {
		t.Fatalf("被丢弃数 = %d，期望 2", st.Discarded)
	}
	if st.AcceptedPages != st.Applied+st.AlreadyApplied+st.SpaceMissing+st.Discarded+st.Buffered {
		t.Fatalf("计数不变式不成立: %+v", st)
	}
}

// Finish 后 Feed、Load 与再次 Finish 都被拒绝；pending 为 0 时 Finish 不算批次。
func TestFinishRejectsEverything(t *testing.T) {
	r := mustNew(t, 1, 1)
	if err := r.Finish(); err != nil {
		t.Fatalf("首次 Finish 失败: %v", err)
	}
	if got := r.Batches(); got != 0 {
		t.Fatalf("pending 为 0 时 Finish 不算批次，batches = %d", got)
	}
	if got := feedErr(t, r, page(1, 1, 1, 0, 1)); got != ErrFinished {
		t.Fatalf("Finish 后 Feed 应报回放已结束，得到 %v", got)
	}
	if err := r.LoadPage(1, 0, 0, 0); err == nil {
		t.Fatalf("Finish 后 Load 应被拒绝")
	} else {
		var rerr *Error
		if !errors.As(err, &rerr) || rerr.Reason != ErrFinished {
			t.Fatalf("Finish 后 Load 应报回放已结束，得到 %v", err)
		}
	}
	if err := r.Finish(); err == nil {
		t.Fatalf("再次 Finish 应被拒绝")
	} else {
		var rerr *Error
		if !errors.As(err, &rerr) || rerr.Reason != ErrFinished {
			t.Fatalf("再次 Finish 应报回放已结束，得到 %v", err)
		}
	}
}

// Load 在 Feed 之后被拒（回放已开始）。
func TestLoadAfterFeedRejected(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 1, 1)
	mustFeed(t, r, file(1, 1, FileCreate, 2))
	err := r.LoadPage(2, 0, 5, 0)
	if err == nil {
		t.Fatalf("Feed 之后 Load 应被拒绝")
	}
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Reason != ErrStarted {
		t.Fatalf("应报回放已开始，得到 %v", err)
	}
	// 被拒绝的 Load 不改变状态。
	checkPage(t, r, 2, 0, 0, 0)
}

// mtr 号回退与 File 夹在未结束 mtr 中的拒绝。
func TestMtrErrors(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustFeed(t, r, page(1, 3, 1, 0, 1))
	mustFeed(t, r, end(2, 3))
	// mtr 号回退：新 Page 的 mtr 不大于此前最大号。
	if got := feedErr(t, r, page(3, 2, 1, 0, 1)); got != ErrMtr {
		t.Fatalf("mtr 回退应报小事务错误，得到 %v", got)
	}
	// File 的 mtr 号同样不得回退。
	if got := feedErr(t, r, file(3, 3, FileCreate, 2)); got != ErrMtr {
		t.Fatalf("File mtr 回退应报小事务错误，得到 %v", got)
	}
	// 开启新 mtr 后，File 不得夹在其中。
	mustFeed(t, r, page(3, 4, 1, 0, 1))
	if got := feedErr(t, r, file(4, 5, FileCreate, 2)); got != ErrMtr {
		t.Fatalf("File 夹在未结束 mtr 中应报小事务错误，得到 %v", got)
	}
	// Page 的 mtr 与未结束的 mtr 不同。
	if got := feedErr(t, r, page(4, 5, 1, 0, 1)); got != ErrMtr {
		t.Fatalf("Page mtr 不一致应报小事务错误，得到 %v", got)
	}
	// End 的 mtr 与未结束的不一致。
	if got := feedErr(t, r, end(4, 9)); got != ErrMtr {
		t.Fatalf("End mtr 不一致应报小事务错误，得到 %v", got)
	}
	mustFeed(t, r, end(4, 4))
	// 没有未结束的 mtr 时 End 被拒绝。
	if got := feedErr(t, r, end(5, 4)); got != ErrMtr {
		t.Fatalf("无未结束 mtr 时 End 应报小事务错误，得到 %v", got)
	}
}

// 被拒绝的记录不推进已接受的最大 LSN（同一 lsn 可重投），也不推进最大 mtr 号。
func TestRejectedDoesNotAdvanceLSN(t *testing.T) {
	r := mustNew(t, 1, 1)
	mustLoad(t, r, 1, 0, 0, 0)
	mustFeed(t, r, page(1, 1, 1, 0, 1))
	mustFeed(t, r, end(2, 1))
	// LSN 不够大。
	if got := feedErr(t, r, page(2, 2, 1, 0, 1)); got != ErrLSNTooSmall {
		t.Fatalf("重复 lsn 应报 LSN 不够大，得到 %v", got)
	}
	if got := feedErr(t, r, page(1, 2, 1, 0, 1)); got != ErrLSNTooSmall {
		t.Fatalf("回退 lsn 应报 LSN 不够大，得到 %v", got)
	}
	// mtr 错误被拒后不推进 lsn：同一 lsn 用正确的 mtr 重投成功。
	if got := feedErr(t, r, page(3, 1, 1, 0, 1)); got != ErrMtr {
		t.Fatalf("mtr 回退应报小事务错误，得到 %v", got)
	}
	mustFeed(t, r, page(3, 2, 1, 0, 1))
	mustFeed(t, r, end(4, 2))
	checkApplyLog(t, r, []ApplyEntry{
		{LSN: 1, Result: Applied},
		{LSN: 3, Result: Applied},
	})
	// File 夹在 mtr 中被拒后不推进 lsn 与最大 mtr 号。
	mustFeed(t, r, page(5, 3, 1, 0, 1))
	if got := feedErr(t, r, file(6, 4, FileCreate, 2)); got != ErrMtr {
		t.Fatalf("File 夹在 mtr 中应报小事务错误，得到 %v", got)
	}
	mustFeed(t, r, end(6, 3))
	// 若 File(6,4) 推进了最大 mtr 号，则 mtr=4 会回退报错；此处应正常接受。
	mustFeed(t, r, page(7, 4, 1, 0, 1))
	mustFeed(t, r, end(8, 4))
}

// 参数非法：构造参数、各号、lsn、mtr、kind 越界。
func TestInvalidParams(t *testing.T) {
	for _, w := range []int{0, -1, 65} {
		if _, err := NewReplayer(w, 1); err == nil {
			t.Fatalf("workers=%d 应报参数非法", w)
		} else {
			var rerr *Error
			if !errors.As(err, &rerr) || rerr.Reason != ErrInvalidParam {
				t.Fatalf("workers=%d 应报参数非法，得到 %v", w, err)
			}
		}
	}
	for _, m := range []int{0, -1, 1_000_001} {
		if _, err := NewReplayer(1, m); err == nil {
			t.Fatalf("batchLimit=%d 应报参数非法", m)
		}
	}
	r := mustNew(t, 1, 1)
	if err := r.LoadPage(1_000_001, 0, 0, 0); err == nil {
		t.Fatalf("space 越界应报参数非法")
	}
	if err := r.LoadPage(0, -1, 0, 0); err == nil {
		t.Fatalf("page 越界应报参数非法")
	}
	if err := r.LoadPage(0, 0, 0, -1); err == nil {
		t.Fatalf("Load lsn 越界应报参数非法")
	}
	if err := r.LoadPage(0, 0, 0, 1_000_000_000_000_001); err == nil {
		t.Fatalf("Load lsn 越界应报参数非法")
	}
	if got := feedErr(t, r, page(0, 1, 1, 0, 1)); got != ErrInvalidParam {
		t.Fatalf("lsn=0 应报参数非法，得到 %v", got)
	}
	if got := feedErr(t, r, page(1, 0, 1, 0, 1)); got != ErrInvalidParam {
		t.Fatalf("mtr<1 应报参数非法，得到 %v", got)
	}
	if got := feedErr(t, r, page(1, 1, 1_000_001, 0, 1)); got != ErrInvalidParam {
		t.Fatalf("space 越界应报参数非法，得到 %v", got)
	}
	if got := feedErr(t, r, file(1, 1, FileKind(7), 1)); got != ErrInvalidParam {
		t.Fatalf("kind 非法应报参数非法，得到 %v", got)
	}
	if got := feedErr(t, r, Record{Type: RecordType(9), LSN: 1, Mtr: 1}); got != ErrInvalidParam {
		t.Fatalf("未知记录类型应报参数非法，得到 %v", got)
	}
	// 参数非法优先于回放已结束。
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	if got := feedErr(t, r, page(0, 1, 1, 0, 1)); got != ErrInvalidParam {
		t.Fatalf("参数非法应优先于回放已结束，得到 %v", got)
	}
}
