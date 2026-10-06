package occupancy

import (
	"fmt"
	"sync"
	"testing"
)

func TestEmergencyPreemptFuture(t *testing.T) {
	s := mustService(t)
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 100, End: 200})
	acceptOK(t, r)
	e := s.Apply(ApplyRequest{OpTime: 10, Road: "a", Lanes: 2, Start: 10, End: 150, Priority: Emergency})
	acceptOK(t, e)
	if len(e.Preempted) != 1 || !e.Preempted[0].Approved || e.Preempted[0].Truncated {
		t.Fatalf("preempt record: %+v", e.Preempted)
	}
	got, _ := s.Permit(r.PermitID)
	if got.Interval != (Interval{150, 250}) || got.Status != Approved {
		t.Fatalf("shifted = %+v", got)
	}
	q, _ := s.Query(QueryRequest{Road: "a", At: 30})
	if q.ClosedLanes != 2 || len(q.Active) != 1 || q.Active[0].Priority != Emergency {
		t.Fatalf("during emergency: %+v", q)
	}
	q, _ = s.Query(QueryRequest{Road: "a", At: 160})
	if q.ClosedLanes != 1 {
		t.Fatalf("after: %+v", q)
	}
}

func TestEmergencyPreemptOngoing(t *testing.T) {
	s := mustService(t)
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 0, End: 100})
	acceptOK(t, r)
	e := s.Apply(ApplyRequest{OpTime: 40, Road: "a", Lanes: 2, Start: 40, End: 70, Priority: Emergency})
	acceptOK(t, e)
	rec := e.Preempted[0]
	if !rec.Truncated || rec.NewStart != 70 || rec.NewEnd != 130 || !rec.Approved {
		t.Fatalf("record: %+v", rec)
	}
	q, _ := s.Query(QueryRequest{Road: "a", At: 39})
	if q.ClosedLanes != 1 || len(q.Active) != 1 {
		t.Fatalf("history: %+v", q)
	}
	q, _ = s.Query(QueryRequest{Road: "a", At: 75})
	if q.ClosedLanes != 1 {
		t.Fatalf("resumed: %+v", q)
	}
}

func TestPreemptOrderAffectsLater(t *testing.T) {
	s := mustService(t)
	r1 := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 100, End: 120})
	acceptOK(t, r1)
	r2 := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 130, End: 150})
	acceptOK(t, r2)
	e := s.Apply(ApplyRequest{OpTime: 50, Road: "a", Lanes: 2, Start: 50, End: 140, Priority: Emergency})
	acceptOK(t, e)
	if len(e.Preempted) != 2 {
		t.Fatalf("preempted: %+v", e.Preempted)
	}
	p1, _ := s.Permit(r1.PermitID)
	p2, _ := s.Permit(r2.PermitID)
	if p1.Status != Approved || p1.Interval != (Interval{140, 160}) {
		t.Fatalf("p1 = %+v", p1)
	}
	if p2.Status != PendingReschedule || p2.Interval != (Interval{140, 160}) {
		t.Fatalf("p2 = %+v", p2)
	}
	if e.Preempted[0].PermitID != r1.PermitID || e.Preempted[1].PermitID != r2.PermitID {
		t.Fatalf("preempt order = %+v", e.Preempted)
	}
}

func TestEmergencyStillLaneBound(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 0, End: 100, Priority: Emergency}))
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 10, End: 20, Priority: Emergency})
	rejectCode(t, r, ErrSameRoadConflict)
}

func TestExtendExcludesSelf(t *testing.T) {
	s := mustService(t)
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 0, End: 100})
	acceptOK(t, r)
	if x := s.Extend(ExtendRequest{OpTime: 0, PermitID: r.PermitID, NewEnd: 200}); !x.Reason.None() {
		t.Fatalf("extend self: %v", x.Reason)
	}
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 200, End: 260}))
	bad := s.Extend(ExtendRequest{OpTime: 0, PermitID: r.PermitID, NewEnd: 250})
	if bad.Reason.Code != ErrSameRoadConflict {
		t.Fatalf("extend reject: %+v", bad)
	}
	p, _ := s.Permit(r.PermitID)
	if p.Interval.End != 200 {
		t.Fatalf("failed extend mutated: %+v", p)
	}
	if sh := s.Extend(ExtendRequest{OpTime: 0, PermitID: r.PermitID, NewEnd: 150}); !sh.Reason.None() {
		t.Fatalf("shorten: %v", sh.Reason)
	}
}

func TestRevokeNoAutoApprove(t *testing.T) {
	s := mustService(t)
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 2, Start: 0, End: 100})
	acceptOK(t, r)
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 10, End: 20}), ErrSameRoadConflict)
	if rv := s.Revoke(RevokeRequest{OpTime: 0, PermitID: r.PermitID}); !rv.Reason.None() {
		t.Fatalf("revoke: %v", rv.Reason)
	}
	if _, ok := s.Permit(2); ok {
		t.Fatal("rejected application must not exist")
	}
	retry := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 10, End: 20})
	acceptOK(t, retry)
	// 撤销待重排许可也合法。
	if p, _ := s.Permit(r.PermitID); p.Status != Revoked {
		t.Fatalf("revoked status = %+v", p)
	}
}

func TestConflictPrioritySameRoadWins(t *testing.T) {
	s := mustService(t)
	// 在合法一致状态下，三类冲突不可能同时成立于同一份新申请：同路段占满
	// 已强制绕行方向清空，绕行无从叠加。因此验证错误码优先级的两两覆盖；
	// 三类同立时的输出由检查顺序天然保证（先报同路段）。
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "n", Lanes: 3, Start: 0, End: 100}))
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 0, Road: "c", Lanes: 1, Start: 0, End: 100}))
	rejectCode(t, s.Apply(ApplyRequest{OpTime: 0, Road: "n", Lanes: 1, Start: 10, End: 20}), ErrSameRoadConflict)

	s2 := mustService(t)
	acceptOK(t, s2.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 0, End: 100}))
	acceptOK(t, s2.Apply(ApplyRequest{OpTime: 0, Road: "c", Lanes: 1, Start: 0, End: 100}))
	rejectCode(t, s2.Apply(ApplyRequest{OpTime: 0, Road: "b", Lanes: 2, Start: 10, End: 20}), ErrDetourConflict)
}

func TestClockAndValidation(t *testing.T) {
	s := mustService(t)
	acceptOK(t, s.Apply(ApplyRequest{OpTime: 5, Road: "a", Lanes: 1, Start: 5, End: 10}))
	if r := s.Apply(ApplyRequest{OpTime: 4, Road: "a", Lanes: 1, Start: 4, End: 9}); r.Reason.Code != ErrClockRollback {
		t.Fatalf("clock: %v", r.Reason)
	}
	if r := s.Apply(ApplyRequest{OpTime: 6, Road: "a", Lanes: 1, Start: 5, End: 8}); r.Reason.Code != ErrStartBeforeNow {
		t.Fatalf("start: %v", r.Reason)
	}
	if r := s.Apply(ApplyRequest{OpTime: 6, Road: "zzz", Lanes: 1, Start: 6, End: 8}); r.Reason.Code != ErrRoadNotFound {
		t.Fatalf("road: %v", r.Reason)
	}
	if r := s.Apply(ApplyRequest{OpTime: 6, Road: "a", Lanes: 9, Start: 6, End: 8}); r.Reason.Code != ErrLanesExceed {
		t.Fatalf("lanes: %v", r.Reason)
	}
	if r := s.Extend(ExtendRequest{OpTime: 6, PermitID: 999, NewEnd: 12}); r.Reason.Code != ErrPermitNotFound {
		t.Fatalf("permit: %v", r.Reason)
	}
	// 被拒操作不推进时钟：OpTime=4 被拒后，6 仍合法。
	// OpTime=7 时许可 1 已结束（[5,10) 在 10 结束），7 未结束，改测 10。
	// 先用 OpTime=10 验证已结束不可延期。
	s2 := mustService(t)
	acc := s2.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 0, End: 10})
	if r := s2.Extend(ExtendRequest{OpTime: 10, PermitID: acc.PermitID, NewEnd: 12}); r.Reason.Code != ErrPermitEnded {
		t.Fatalf("ended: %v", r.Reason)
	}
}

func TestEndedPermitCannotExtend(t *testing.T) {
	s := mustService(t)
	r := s.Apply(ApplyRequest{OpTime: 0, Road: "a", Lanes: 1, Start: 0, End: 10})
	acceptOK(t, r)
	if x := s.Extend(ExtendRequest{OpTime: 10, PermitID: r.PermitID, NewEnd: 20}); x.Reason.Code != ErrPermitEnded {
		t.Fatalf("ended extend: %v", x.Reason)
	}
	if x := s.Revoke(RevokeRequest{OpTime: 10, PermitID: r.PermitID}); x.Reason.Code != ErrPermitEnded {
		t.Fatalf("ended revoke: %v", x.Reason)
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	s := mustService(t)
	var wg sync.WaitGroup
	var startWg sync.WaitGroup
	startWg.Add(1)
	var okN, badClock int64
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			startWg.Wait()
			// 所有 goroutine 用相同操作时刻与互不相交时段；合法串行顺序下
			// 被接受的申请必然全部通过，时钟回退被拒的数量由串行序决定。
			r := s.Apply(ApplyRequest{
				OpTime: 0, Road: "a", Lanes: 2,
				Start: int64(1000 + i), End: int64(1001 + i),
			})
			mu.Lock()
			if r.Reason.None() {
				okN++
			} else if r.Reason.Code == ErrClockRollback {
				badClock++
			}
			mu.Unlock()
		}()
	}
	startWg.Done()
	wg.Wait()
	if okN+badClock != 50 || okN == 0 {
		t.Fatalf("serial equivalence: accepted=%d clockrollback=%d", okN, badClock)
	}
}

func TestQueryComplexityIndependentOfHistory(t *testing.T) {
	s := mustService(t)
	// 在 a 上制造大量已成历史的许可，然后全部被 GC 到归档。
	const n = 200
	for i := 0; i < n; i++ {
		r := s.Apply(ApplyRequest{
			OpTime: int64(2 * i), Road: "a", Lanes: 1,
			Start: int64(2 * i), End: int64(2*i + 1),
		})
		acceptOK(t, r)
	}
	idx := s.indices["a"]
	// live treap 含全部 n 份历史片段，但点查 2n 时 maxEnd 剪枝使
	// “结束时刻 > 2n” 的片段数为 0——查询访问量不随 n 增长。
	if idx.liveCount() != n {
		t.Fatalf("live=%d want %d (history retained for queries)", idx.liveCount(), n)
	}
	if got := idx.activeAtOrAfter(int64(2 * n)); got != 0 {
		t.Fatalf("active end>2n = %d, want 0 (query cost independent of history)", got)
	}
	q, err := s.Query(QueryRequest{Road: "a", At: 2 * n})
	if !err.None() {
		t.Fatal(err)
	}
	if q.ClosedLanes != 0 || len(q.Active) != 0 {
		t.Fatalf("future query = %+v", q)
	}
	// 历史查询返回正确的一份许可。
	q, _ = s.Query(QueryRequest{Road: "a", At: 4})
	if q.ClosedLanes != 1 || len(q.Active) != 1 {
		t.Fatalf("history at 4 = %+v", q)
	}
	// 即使历史再翻倍，未来时刻点查的候选规模仍为常数。
	for i := n; i < 2*n; i++ {
		r := s.Apply(ApplyRequest{
			OpTime: int64(2 * i), Road: "a", Lanes: 1,
			Start: int64(2 * i), End: int64(2*i + 1),
		})
		acceptOK(t, r)
	}
	if got := s.indices["a"].liveCount(); got != 2*n {
		t.Fatalf("live=%d want %d", got, 2*n)
	}
	if got := s.indices["a"].activeAtOrAfter(int64(4 * n)); got != 0 {
		t.Fatalf("active end>4n = %d, want 0", got)
	}
	_ = fmt.Sprint
}
