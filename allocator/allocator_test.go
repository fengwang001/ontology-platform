package allocator

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, r, a int) *Allocator {
	t.Helper()
	al, err := New(r, a)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", r, a, err)
	}
	return al
}

func mustAdd(t *testing.T, a *Allocator, ids ...int64) {
	t.Helper()
	if err := a.AddSplits(ids); err != nil {
		t.Fatalf("AddSplits(%v): %v", ids, err)
	}
}

func wantAssigned(t *testing.T, a *Allocator, r int, want int64) {
	t.Helper()
	rep, err := a.RequestSplit(r)
	if err != nil {
		t.Fatalf("RequestSplit(%d): %v", r, err)
	}
	if rep.Kind != ReplyAssigned || rep.Split != want {
		t.Fatalf("RequestSplit(%d) = %v, want ASSIGNED(%d)", r, rep, want)
	}
}

func wantReply(t *testing.T, a *Allocator, r int, want ReplyKind) {
	t.Helper()
	rep, err := a.RequestSplit(r)
	if err != nil {
		t.Fatalf("RequestSplit(%d): %v", r, err)
	}
	if rep.Kind != want {
		t.Fatalf("RequestSplit(%d) = %v, want %v", r, rep.Kind, want)
	}
}

func wantErr(t *testing.T, err error, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func wantState(t *testing.T, a *Allocator, s int64, st State, owner, ret int) {
	t.Helper()
	info, ok := a.State(s)
	if !ok {
		t.Fatalf("State(%d): not registered", s)
	}
	if info.State != st || info.Owner != owner || info.Ret != ret {
		t.Fatalf("State(%d) = %+v, want {%v %d %d}", s, info, st, owner, ret)
	}
}

// TestSpecExample 逐步重放题目中的示例。
func TestSpecExample(t *testing.T) {
	a := mustNew(t, 2, 2)
	mustAdd(t, a, 0, 1, 2, 3, 4)

	wantAssigned(t, a, 0, 0)
	wantAssigned(t, a, 1, 1)
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	wantAssigned(t, a, 0, 2) // at=2
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 0); err != nil { // ft=2
		t.Fatal(err)
	}
	if err := a.Checkpoint(2); err != nil {
		t.Fatal(err)
	}
	wantAssigned(t, a, 1, 3) // at=3

	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Returned, []int64{2}) {
		t.Fatalf("returned = %v, want [2]", f.Returned)
	}
	if !reflect.DeepEqual(f.Quarantined, []int64{}) {
		t.Fatalf("quarantined = %v, want []", f.Quarantined)
	}
	if !reflect.DeepEqual(f.Revoked, []int64{0}) {
		t.Fatalf("revoked = %v, want [0]", f.Revoked)
	}
	wantState(t, a, 0, StateAssigned, 0, 0)
	wantState(t, a, 2, StateUnassigned, -1, 1)

	wantAssigned(t, a, 1, 2) // 偷取偏好读取器 0（已失败）的最小者
	wantAssigned(t, a, 1, 4)
	wantReply(t, a, 1, ReplyWait)
	a.Seal()
	wantReply(t, a, 1, ReplyWait) // 尚有 ASSIGNED 拆分
	if err := a.Complete(2); err != nil {
		t.Fatal(err)
	}
	_, err = a.RequestSplit(0)
	wantErr(t, err, ErrReaderFailed)
}

// TestPreferredSmallest 偏好分配取偏好桶中编号最小者，与登记顺序无关。
func TestPreferredSmallest(t *testing.T) {
	a := mustNew(t, 3, 2)
	mustAdd(t, a, 9, 1, 6, 4, 0, 3, 8)
	wantAssigned(t, a, 0, 0)
	wantAssigned(t, a, 0, 3)
	wantAssigned(t, a, 0, 6)
	wantAssigned(t, a, 0, 9)
	wantAssigned(t, a, 1, 1)
	wantAssigned(t, a, 1, 4)
	wantAssigned(t, a, 2, 8)
}

// TestStealOnlyFailedPreferred 偷取只取偏好读取器已失败的拆分。
func TestStealOnlyFailedPreferred(t *testing.T) {
	a := mustNew(t, 3, 2)
	mustAdd(t, a, 0, 1, 2, 3, 4, 5)
	// 读空偏好为 2 的桶。
	wantAssigned(t, a, 2, 2)
	wantAssigned(t, a, 2, 5)
	// 读取器 0、1 存活：读取器 2 无可偷，等待。
	wantReply(t, a, 2, ReplyWait)
	// 读取器 1 失败：可偷偏好为 1 的最小者。
	if _, err := a.ReaderFailed(1); err != nil {
		t.Fatal(err)
	}
	wantAssigned(t, a, 2, 1)
	wantAssigned(t, a, 2, 4)
	// 读取器 0 仍存活：其偏好桶不可被偷。
	wantReply(t, a, 2, ReplyWait)
	// 读取器 0 失败后可偷。
	if _, err := a.ReaderFailed(0); err != nil {
		t.Fatal(err)
	}
	wantAssigned(t, a, 2, 0)
	wantAssigned(t, a, 2, 3)
}

// TestEpochTriggeredNotCompleted 纪元按「触发」计：分配发生在已触发但尚未
// 完成的检查点之前（at <= lt 且 at > done），失败仍要归还。
func TestEpochTriggeredNotCompleted(t *testing.T) {
	a := mustNew(t, 1, 5)
	mustAdd(t, a, 7)
	wantAssigned(t, a, 0, 7) // at = E = 1
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	// 此时 lt=1，at=1 <= lt，但 done=0 < at：仍属「恢复状态里没有它」。
	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Returned, []int64{7}) {
		t.Fatalf("returned = %v, want [7]", f.Returned)
	}
	wantState(t, a, 7, StateUnassigned, -1, 1)
}

// TestAtBoundary at 恰等于 done 时保留，差 1（done+1）时归还。
func TestAtBoundary(t *testing.T) {
	a := mustNew(t, 1, 5)
	mustAdd(t, a, 0, 1)
	wantAssigned(t, a, 0, 0) // at=1
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil { // done=1
		t.Fatal(err)
	}
	wantAssigned(t, a, 0, 1) // at=2
	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Returned, []int64{1}) || len(f.Revoked) != 0 {
		t.Fatalf("failure = %+v, want returned [1]", f)
	}
	wantState(t, a, 0, StateAssigned, 0, 0)    // at == done，保留
	wantState(t, a, 1, StateUnassigned, -1, 1) // at == done+1，归还
}

// TestFtBoundary ft 恰等于 done 时保留完成，大 1 时撤销。
func TestFtBoundary(t *testing.T) {
	a := mustNew(t, 1, 5)
	mustAdd(t, a, 0, 1)
	wantAssigned(t, a, 0, 0)                 // at=1
	wantAssigned(t, a, 0, 1)                 // at=1
	if err := a.Finished(0, 0); err != nil { // ft=1
		t.Fatal(err)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 1); err != nil { // ft=2
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil { // done=1
		t.Fatal(err)
	}
	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Revoked, []int64{1}) || len(f.Returned) != 0 {
		t.Fatalf("failure = %+v, want revoked [1]", f)
	}
	wantState(t, a, 0, StateFinished, 0, 0) // ft == done，完成保留
	wantState(t, a, 1, StateAssigned, 0, 0) // ft == done+1，撤销完成
}

// TestFinishedButAtAfterDoneReturned 已 FINISHED 但 at > done 的拆分被归还，
// 且丢掉完成状态。
func TestFinishedButAtAfterDoneReturned(t *testing.T) {
	a := mustNew(t, 1, 5)
	mustAdd(t, a, 0)
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil { // done=1
		t.Fatal(err)
	}
	wantAssigned(t, a, 0, 0)                 // at=2
	if err := a.Finished(0, 0); err != nil { // ft=2
		t.Fatal(err)
	}
	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Returned, []int64{0}) || len(f.Revoked) != 0 {
		t.Fatalf("failure = %+v, want returned [0]", f)
	}
	wantState(t, a, 0, StateUnassigned, -1, 1) // 完成状态丢失
}

// TestQuarantineThreshold ret 恰达到 A 时隔离，差 1 时回池。
func TestQuarantineThreshold(t *testing.T) {
	a := mustNew(t, 1, 2)
	mustAdd(t, a, 0)

	// 第一次失败：ret=1 < A=2，回池。
	wantAssigned(t, a, 0, 0)
	f, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Returned, []int64{0}) || len(f.Quarantined) != 0 {
		t.Fatalf("failure = %+v, want returned [0]", f)
	}
	wantState(t, a, 0, StateUnassigned, -1, 1)
	if got := a.Quarantined(); len(got) != 0 {
		t.Fatalf("Quarantined() = %v, want empty", got)
	}

	// 第二次失败：ret=2 = A，隔离（终态）。
	if err := a.ReaderRestarted(0); err != nil {
		t.Fatal(err)
	}
	wantAssigned(t, a, 0, 0)
	f, err = a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Quarantined, []int64{0}) || len(f.Returned) != 0 {
		t.Fatalf("failure = %+v, want quarantined [0]", f)
	}
	wantState(t, a, 0, StateQuarantined, -1, 2)
	if got := a.Quarantined(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("Quarantined() = %v, want [0]", got)
	}

	// 隔离后不再分配：重启后也只能等待。
	if err := a.ReaderRestarted(0); err != nil {
		t.Fatal(err)
	}
	wantReply(t, a, 0, ReplyWait)
	a.Seal()
	wantReply(t, a, 0, ReplyNoMore) // 隔离不阻止「无更多拆分」
}

// TestRetainedNotStolen 失败读取器名下保留的拆分（at <= done）不被偷取，
// 也不分配给任何人，直到该读取器重新存活。
func TestRetainedNotStolen(t *testing.T) {
	a := mustNew(t, 2, 5)
	mustAdd(t, a, 0, 2, 4) // 均偏好读取器 0
	wantAssigned(t, a, 0, 0)
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil { // done=1，拆分 0 的 at=1 <= done
		t.Fatal(err)
	}
	if _, err := a.ReaderFailed(0); err != nil {
		t.Fatal(err)
	}
	// 拆分 0 保留在失败的读取器 0 名下；2、4 可被读取器 1 偷取，
	// 但 0 绝不出现。
	wantAssigned(t, a, 1, 2)
	wantAssigned(t, a, 1, 4)
	wantReply(t, a, 1, ReplyWait)
	wantState(t, a, 0, StateAssigned, 0, 0)
	// 重启后仍归读取器 0 所有，可由其完成。
	if err := a.ReaderRestarted(0); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	wantState(t, a, 0, StateFinished, 0, 0)
}

// TestNoMoreConditions 「无更多拆分」要求封口且无 UNASSIGNED 且无 ASSIGNED。
func TestNoMoreConditions(t *testing.T) {
	a := mustNew(t, 2, 2)
	mustAdd(t, a, 0, 1)
	wantAssigned(t, a, 0, 0)
	wantAssigned(t, a, 1, 1)

	// 未封口：等待。
	wantReply(t, a, 0, ReplyWait)
	a.Seal()
	// 已封口但有 ASSIGNED：等待。
	wantReply(t, a, 0, ReplyWait)

	// 完成全部后：无更多拆分。
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(1, 1); err != nil {
		t.Fatal(err)
	}
	wantReply(t, a, 0, ReplyNoMore)
	wantReply(t, a, 1, ReplyNoMore)

	// 有 UNASSIGNED 时即使封口也是等待。
	b := mustNew(t, 1, 2)
	mustAdd(t, b, 5)
	b.Seal()
	wantReply(t, b, 0, ReplyAssigned)

	// 失败读取器名下的 ASSIGNED 也阻止「无更多拆分」。
	c := mustNew(t, 2, 2)
	mustAdd(t, c, 0)
	wantAssigned(t, c, 0, 0) // at=1
	if err := c.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := c.Complete(1); err != nil { // done=1，拆分 0 的 at=1 <= done
		t.Fatal(err)
	}
	if _, err := c.ReaderFailed(0); err != nil { // 保留在失败的读取器 0 名下
		t.Fatal(err)
	}
	c.Seal()
	wantReply(t, c, 1, ReplyWait)
}

// TestAddSplitsDuplicateAtomic AddSplits 编号重复整体不生效（含与隔离过的
// 编号重复），且拒绝顺序为：参数非法、已封口、编号重复。
func TestAddSplitsDuplicateAtomic(t *testing.T) {
	a := mustNew(t, 1, 1)
	mustAdd(t, a, 0, 1)

	// 列表内重复：整体不生效。
	wantErr(t, a.AddSplits([]int64{5, 6, 5}), ErrDuplicateSplit)
	if _, ok := a.State(5); ok {
		t.Fatal("split 5 must not be registered")
	}
	if _, ok := a.State(6); ok {
		t.Fatal("split 6 must not be registered")
	}

	// 与已登记编号重复：整体不生效。
	wantErr(t, a.AddSplits([]int64{7, 0}), ErrDuplicateSplit)
	if _, ok := a.State(7); ok {
		t.Fatal("split 7 must not be registered")
	}

	// 与隔离过的编号重复：同样拒绝。
	wantAssigned(t, a, 0, 0)
	if _, err := a.ReaderFailed(0); err != nil { // A=1，一次归还即隔离
		t.Fatal(err)
	}
	wantState(t, a, 0, StateQuarantined, -1, 1)
	wantErr(t, a.AddSplits([]int64{9, 0}), ErrDuplicateSplit)
	if _, ok := a.State(9); ok {
		t.Fatal("split 9 must not be registered")
	}

	// 参数非法优先于已封口。
	a.Seal()
	wantErr(t, a.AddSplits(nil), ErrInvalidArgument)
	wantErr(t, a.AddSplits(make([]int64, 1001)), ErrInvalidArgument)
	wantErr(t, a.AddSplits([]int64{-1}), ErrInvalidArgument)
	wantErr(t, a.AddSplits([]int64{1_000_000_001}), ErrInvalidArgument)
	// 已封口优先于编号重复。
	wantErr(t, a.AddSplits([]int64{0}), ErrSealed)
	// 边界：1e9 合法。
	b := mustNew(t, 1, 1)
	if err := b.AddSplits([]int64{1_000_000_000}); err != nil {
		t.Fatal(err)
	}
}

// TestCompleteOrder Complete 的过期与超前，及参数非法优先。
func TestCompleteOrder(t *testing.T) {
	a := mustNew(t, 1, 1)
	wantErr(t, a.Complete(0), ErrInvalidArgument)
	wantErr(t, a.Complete(-3), ErrInvalidArgument)
	wantErr(t, a.Complete(1), ErrCompleteAhead) // lt=0
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Checkpoint(2); err != nil {
		t.Fatal(err)
	}
	wantErr(t, a.Complete(3), ErrCompleteAhead)
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	wantErr(t, a.Complete(1), ErrCompleteStale)
	wantErr(t, a.Complete(0), ErrInvalidArgument) // 参数非法优先于过期
	if err := a.Complete(2); err != nil {
		t.Fatal(err)
	}
	// Checkpoint 乱序。
	wantErr(t, a.Checkpoint(4), ErrCheckpointOutOfOrder)
	wantErr(t, a.Checkpoint(2), ErrCheckpointOutOfOrder)
	wantErr(t, a.Checkpoint(0), ErrCheckpointOutOfOrder)
	if err := a.Checkpoint(3); err != nil {
		t.Fatal(err)
	}
}

// TestRejectedNoStateChange 被拒绝的操作不得改变任何状态。
func TestRejectedNoStateChange(t *testing.T) {
	snapshot := func(a *Allocator) string {
		c := a.Counts()
		out := fmt.Sprintf("counts=%v quar=%v lt=%d done=%d sealed=%v",
			c, a.Quarantined(), a.lt, a.done, a.sealed)
		for s := int64(0); s < 6; s++ {
			if info, ok := a.State(s); ok {
				out += fmt.Sprintf(" s%d=%v/%d/%d", s, info.State, info.Owner, info.Ret)
			}
		}
		return out
	}

	a := mustNew(t, 2, 2)
	mustAdd(t, a, 0, 1, 2, 3, 4, 5)
	wantAssigned(t, a, 0, 0)
	wantAssigned(t, a, 1, 1)
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	before := snapshot(a)

	// 各类拒绝。
	wantErr(t, a.AddSplits([]int64{0}), ErrDuplicateSplit)
	wantErr(t, a.AddSplits([]int64{1_000_000_001}), ErrInvalidArgument)
	wantErr(t, a.Checkpoint(3), ErrCheckpointOutOfOrder)
	wantErr(t, a.Complete(0), ErrInvalidArgument)
	wantErr(t, a.Complete(2), ErrCompleteAhead)
	wantErr(t, a.Complete(1), ErrCompleteStale)
	wantErr(t, a.Finished(0, 1), ErrNotAssignedToReader)
	wantErr(t, a.Finished(0, 99), ErrNotAssignedToReader)
	wantErr(t, a.Finished(7, 0), ErrInvalidArgument)
	if _, err := a.ReaderFailed(0); err != nil { // 合法：先让读取器 0 失败
		t.Fatal(err)
	}
	wantErr(t, a.ReaderRestarted(1), ErrReaderNotFailed)
	_, err := a.RequestSplit(0)
	wantErr(t, err, ErrReaderFailed)
	wantErr(t, a.Finished(0, 0), ErrReaderFailed)
	_, err = a.ReaderFailed(0)
	wantErr(t, err, ErrReaderFailed)
	wantErr(t, a.ReaderRestarted(-1), ErrInvalidArgument)

	// 只有合法的 ReaderFailed(0) 改变了状态；其余拒绝均未生效。
	after := snapshot(a)
	want := "counts={4 2 0 0} quar=[] lt=1 done=1 sealed=false" +
		" s0=ASSIGNED/0/0 s1=ASSIGNED/1/0 s2=UNASSIGNED/-1/0 s3=UNASSIGNED/-1/0 s4=UNASSIGNED/-1/0 s5=UNASSIGNED/-1/0"
	if after != want {
		t.Fatalf("snapshot = %q, want %q", after, want)
	}
	_ = before
}

// addSplitsChunked 以每批 1000 个登记 splits，返回实际登记的编号。
func addSplitsChunked(t *testing.T, a *Allocator, ids []int64) {
	t.Helper()
	for len(ids) > 0 {
		n := min(1000, len(ids))
		if err := a.AddSplits(ids[:n]); err != nil {
			t.Fatalf("AddSplits: %v", err)
		}
		ids = ids[n:]
	}
}

// idsSkippingPref 生成 n 个偏好读取器不为 0 的编号。
func idsSkippingPref(n, r int) []int64 {
	ids := make([]int64, 0, n)
	for id := int64(1); len(ids) < n; id++ {
		if int(id%int64(r)) != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// TestProbesCounter RequestSplit 每次查看的候选堆顶数不超过 R+1，
// 且与未分配拆分总数无关。
func TestProbesCounter(t *testing.T) {
	const r = 4
	build := func(t *testing.T, n int) *Allocator {
		a := mustNew(t, r, 2)
		addSplitsChunked(t, a, idsSkippingPref(n, r)) // 读取器 0 的偏好堆为空
		if _, err := a.ReaderFailed(1); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ReaderFailed(2); err != nil {
			t.Fatal(err)
		}
		return a
	}

	probesOf := func(a *Allocator) int64 {
		before := a.probes
		rep, err := a.RequestSplit(0) // 偷取：查看本堆 + 2 个失败读取器的堆
		if err != nil {
			t.Fatal(err)
		}
		if rep.Kind != ReplyAssigned {
			t.Fatalf("reply = %v, want ASSIGNED", rep.Kind)
		}
		return a.probes - before
	}

	small := probesOf(build(t, 10))
	large := probesOf(build(t, 100000))
	if small != large {
		t.Fatalf("probes differ by scale: small=%d large=%d", small, large)
	}
	if small > int64(r)+1 {
		t.Fatalf("probes = %d, want <= R+1 = %d", small, r+1)
	}
	t.Logf("probes per RequestSplit: small=%d large=%d (R+1=%d)", small, large, r+1)

	// 本堆命中时只查看 1 个堆顶，与规模无关。
	hit := func(n int) int64 {
		a := mustNew(t, r, 2)
		addSplitsChunked(t, a, idsSkippingPref(n, r))
		if err := a.AddSplits([]int64{0}); err != nil { // 偏好读取器 0
			t.Fatal(err)
		}
		before := a.probes
		if _, err := a.RequestSplit(0); err != nil {
			t.Fatal(err)
		}
		return a.probes - before
	}
	if hit(10) != 1 || hit(100000) != 1 {
		t.Fatalf("own-heap hit probes = %d/%d, want 1/1", hit(10), hit(100000))
	}
}

// TestOwnedCounter ReaderFailed 的 owned 增量恰等于该读取器名下
// ASSIGNED+FINISHED 拆分数，与全部拆分总数无关。
func TestOwnedCounter(t *testing.T) {
	build := func(t *testing.T, total int) *Allocator {
		a := mustNew(t, 2, 100)
		ids := make([]int64, total)
		for i := range ids {
			ids[i] = int64(i)
		}
		addSplitsChunked(t, a, ids)
		// 读取器 0 名下：2 个 ASSIGNED + 1 个 FINISHED = 3 个。
		wantAssigned(t, a, 0, 0)
		wantAssigned(t, a, 0, 2)
		wantAssigned(t, a, 0, 4)
		if err := a.Finished(0, 4); err != nil {
			t.Fatal(err)
		}
		return a
	}
	ownedOf := func(a *Allocator) int64 {
		before := a.owned
		f, err := a.ReaderFailed(0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(f.Returned) + len(f.Quarantined) + len(f.Revoked); got != 3 {
			t.Fatalf("processed = %d, want 3", got)
		}
		return a.owned - before
	}
	small := ownedOf(build(t, 10))
	large := ownedOf(build(t, 100000))
	if small != 3 || large != 3 {
		t.Fatalf("owned delta = %d/%d, want 3/3", small, large)
	}
	t.Logf("owned delta: total=10 -> %d, total=100000 -> %d", small, large)
}

// TestConcurrent 并发调用所有操作，等价于某个串行顺序；结束后校验不变量。
func TestConcurrent(t *testing.T) {
	const (
		r      = 8
		splits = 20000
	)
	a := mustNew(t, r, 3)
	ids := make([]int64, splits)
	for i := range ids {
		ids[i] = int64(i)
	}
	addSplitsChunked(t, a, ids)

	stop := make(chan struct{})
	var wg sync.WaitGroup  // 工作协程
	var cwg sync.WaitGroup // 检查点推进器
	cwg.Add(1)
	go func() { // 检查点推进器
		defer cwg.Done()
		cp := int64(1)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := a.Checkpoint(cp); err == nil {
				_ = a.Complete(cp)
				cp++
			}
		}
	}()

	for reader := 0; reader < r; reader++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			seed := int64(reader*131 + 7)
			for i := 0; i < 3000; i++ {
				seed = seed*6364136223846793005 + 1442695040888963407
				switch (seed >> 33) % 6 {
				case 0, 1, 2:
					rep, err := a.RequestSplit(reader)
					if err == nil && rep.Kind == ReplyAssigned {
						_ = a.Finished(reader, rep.Split)
					}
				case 3:
					_, _ = a.ReaderFailed(reader)
				case 4:
					_ = a.ReaderRestarted(reader)
				case 5:
					_, _ = a.State(int64((seed >> 20) % splits))
					_ = a.Counts()
					_ = a.Quarantined()
				}
			}
		}(reader)
	}
	wg.Wait()
	close(stop)
	cwg.Wait()

	// 不变量校验（此时已无并发）。
	c := a.Counts()
	total := c.Unassigned + c.Assigned + c.Finished + c.Quarantined
	if total != splits {
		t.Fatalf("counts sum = %d, want %d", total, splits)
	}
	if a.done > a.lt {
		t.Fatalf("done=%d > lt=%d", a.done, a.lt)
	}
	scan := Counts{}
	for id := int64(0); id < splits; id++ {
		info, ok := a.State(id)
		if !ok {
			t.Fatalf("split %d missing", id)
		}
		switch info.State {
		case StateUnassigned, StateQuarantined:
			if info.Owner != -1 {
				t.Fatalf("split %d state=%v owner=%d, want -1", id, info.State, info.Owner)
			}
		case StateAssigned, StateFinished:
			if info.Owner < 0 || info.Owner >= r {
				t.Fatalf("split %d state=%v owner=%d", id, info.State, info.Owner)
			}
		}
		if info.State == StateQuarantined && info.Ret < 3 {
			t.Fatalf("split %d quarantined with ret=%d < A", id, info.Ret)
		}
		if info.Ret > 3 {
			t.Fatalf("split %d ret=%d > A (ret 只在 ReaderFailed 时增加)", id, info.Ret)
		}
		switch info.State {
		case StateUnassigned:
			scan.Unassigned++
		case StateAssigned:
			scan.Assigned++
		case StateFinished:
			scan.Finished++
		case StateQuarantined:
			scan.Quarantined++
		}
	}
	if scan != c {
		t.Fatalf("Counts() = %+v, scan = %+v", c, scan)
	}
	quar := a.Quarantined()
	if len(quar) != c.Quarantined {
		t.Fatalf("Quarantined() len = %d, counts = %d", len(quar), c.Quarantined)
	}
	for i := 1; i < len(quar); i++ {
		if quar[i] <= quar[i-1] {
			t.Fatalf("Quarantined() not sorted: %v", quar)
		}
	}
}

// TestDeterminismReplay 相同操作序列重放得到完全相同的返回值与状态。
func TestDeterminismReplay(t *testing.T) {
	run := func() []string {
		a := mustNew(t, 3, 2)
		var out []string
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		mustAdd(t, a, 0, 1, 2, 3, 4, 5, 6, 7, 8)
		for i := 0; i < 3; i++ {
			rep, err := a.RequestSplit(i)
			record("req %d -> %v %v", i, rep, err)
		}
		record("ckpt -> %v", a.Checkpoint(1))
		record("complete -> %v", a.Complete(1))
		record("fin -> %v", a.Finished(0, 0))
		f, err := a.ReaderFailed(1)
		record("fail 1 -> %+v %v", f, err)
		rep, err := a.RequestSplit(2)
		record("req 2 -> %v %v", rep, err)
		record("restart -> %v", a.ReaderRestarted(1))
		a.Seal()
		rep, err = a.RequestSplit(1)
		record("req 1 -> %v %v", rep, err)
		record("counts -> %v", a.Counts())
		record("quar -> %v", a.Quarantined())
		for s := int64(0); s < 9; s++ {
			info, ok := a.State(s)
			record("state %d -> %v %v", s, info, ok)
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\n%v\nvs\n%v", first, second)
	}
}
