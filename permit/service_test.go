package permit_test

import (
	"testing"

	"ontology/permit"
)

// 测试路网：
//
//	A(3 车道,走廊 C1) 绕行 [B,C]
//	B(2 车道,走廊 C1) 绕行 [A]
//	C(2 车道,走廊 C2) 绕行 [A]
//	D(2 车道,走廊 C2) 绕行 [C]
func testNetwork() (permit.Network, map[string]int) {
	n := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 3, Corridor: "C1", Detour: []string{"B", "C"}},
		{ID: "B", Lanes: 2, Corridor: "C1", Detour: []string{"A"}},
		{ID: "C", Lanes: 2, Corridor: "C2", Detour: []string{"A"}},
		{ID: "D", Lanes: 2, Corridor: "C2", Detour: []string{"C"}},
	}}
	return n, map[string]int{"C1": 2, "C2": 2}
}

func mustApply(t *testing.T, s *permit.Service, r permit.ApplyRequest) {
	t.Helper()
	res := s.Apply(r)
	if res.Err != nil {
		t.Fatalf("apply %s unexpected error: %v", r.ID, res.Err)
	}
}

// 车道数之和恰等于车道数允许。
func TestLanesSumEqualsCapacity(t *testing.T) {
	n, capm := testNetwork()
	s, err := permit.NewService(n, capm)
	if err != nil {
		t.Fatal(err)
	}
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p1", Segment: "A", Lanes: 2, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p2", Segment: "A", Lanes: 1, Start: 10, End: 20})
	res := s.Apply(permit.ApplyRequest{OpAt: 1, ID: "p3", Segment: "A", Lanes: 1, Start: 10, End: 20})
	if res.Err == nil || res.Err.Code != permit.ErrSameSegment {
		t.Fatalf("expected same-segment conflict, got %+v", res.Err)
	}
	q := s.Query(permit.QueryRequest{Segment: "A", At: 15})
	if q.Closed != 3 || len(q.ActiveIDs) != 2 {
		t.Fatalf("query mismatch: %+v", q)
	}
}

// 时段首尾相接不视为相交。
func TestIntervalAdjacencyNoConflict(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p1", Segment: "A", Lanes: 3, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p2", Segment: "A", Lanes: 3, Start: 20, End: 30})
	if q := s.Query(permit.QueryRequest{Segment: "A", At: 19}); q.Closed != 3 || len(q.ActiveIDs) != 1 {
		t.Fatalf("at 19: %+v", q)
	}
	if q := s.Query(permit.QueryRequest{Segment: "A", At: 20}); q.Closed != 3 || q.ActiveIDs[0] != "p2" {
		t.Fatalf("at 20: %+v", q)
	}
}

// 绕行冲突两个方向，且申请次序不影响结果。
func TestDetourBothDirections(t *testing.T) {
	for _, order := range []int{0, 1} {
		n, capm := testNetwork()
		s, _ := permit.NewService(n, capm)
		full := permit.ApplyRequest{OpAt: 1, ID: "fullA", Segment: "A", Lanes: 3, Start: 10, End: 20}
		onB := permit.ApplyRequest{OpAt: 1, ID: "onB", Segment: "B", Lanes: 1, Start: 12, End: 18}
		if order == 0 {
			mustApply(t, s, full)
		} else {
			mustApply(t, s, onB)
		}
		var res *permit.AcceptResult
		if order == 0 {
			res = s.Apply(onB)
		} else {
			res = s.Apply(full)
		}
		if res.Err == nil || res.Err.Code != permit.ErrDetour {
			t.Fatalf("order=%d expected detour conflict, got %+v", order, res.Err)
		}
	}
}

// 走廊并发恰等于上限允许，超过则拒绝。
func TestCorridorExactlyAtCap(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "a", Segment: "A", Lanes: 1, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "b", Segment: "B", Lanes: 1, Start: 10, End: 20})
	res := s.Apply(permit.ApplyRequest{OpAt: 1, ID: "a2", Segment: "A", Lanes: 1, Start: 10, End: 12})
	if res.Err == nil || res.Err.Code != permit.ErrCorridorCap {
		t.Fatalf("expected corridor cap, got %+v", res.Err)
	}
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "a3", Segment: "A", Lanes: 1, Start: 20, End: 30})
}

// 应急抢占未开始的常规许可。
func TestEmergencyPreemptNotStarted(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r", Segment: "A", Lanes: 2, Start: 15, End: 25})
	res := s.Apply(permit.ApplyRequest{OpAt: 10, ID: "e", Segment: "A", Lanes: 3,
		Start: 10, End: 20, Priority: permit.Emergency})
	if res.Err != nil {
		t.Fatalf("emergency should be accepted: %v", res.Err)
	}
	if len(res.PreemptedIDs) != 1 || res.PreemptedIDs[0] != "r" {
		t.Fatalf("expected r preempted, got %+v", res.PreemptedIDs)
	}
	if !res.Reschedule["r"] {
		t.Fatalf("r should pass deferred review")
	}
	if got := res.RescheduledInterval["r"]; got != (permit.Interval{Start: 20, End: 30}) {
		t.Fatalf("r should shift to [20,30), got %+v", got)
	}
	r, _ := s.Permit("r")
	if r.Status != permit.StatusApproved {
		t.Fatalf("r should be approved again, got %s", r.Status)
	}
	if h := r.History; h[1].Kind != "PREEMPT" || h[2].Kind != "RESCHEDULE_APPROVED" {
		t.Fatalf("unexpected history: %+v", h)
	}
}

// 应急抢占已开始的常规许可：截断剩余时段后顺延。
func TestEmergencyPreemptAlreadyStarted(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r", Segment: "A", Lanes: 2, Start: 10, End: 20})
	res := s.Apply(permit.ApplyRequest{OpAt: 15, ID: "e", Segment: "A", Lanes: 3,
		Start: 15, End: 18, Priority: permit.Emergency})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := res.RescheduledInterval["r"]; got != (permit.Interval{Start: 18, End: 23}) {
		t.Fatalf("unexpected shifted interval: %+v", got)
	}
}

// 多份被抢占许可按原批准次序顺延，前者恢复后使后者失败（同路段车道）。
func TestEarlierRecoveryBlocksLater(t *testing.T) {
	n := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 2, Corridor: "C1"},
	}}
	s, _ := permit.NewService(n, map[string]int{"C1": 10})
	// 两份许可原本时段不相交（r1 占 2 车道，r2 随后占 1 车道），可各自获批。
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r1", Segment: "A", Lanes: 2, Start: 10, End: 12})
	mustApply(t, s, permit.ApplyRequest{OpAt: 2, ID: "r2", Segment: "A", Lanes: 1, Start: 12, End: 13})
	res := s.Apply(permit.ApplyRequest{OpAt: 5, ID: "e", Segment: "A", Lanes: 2,
		Start: 11, End: 14, Priority: permit.Emergency})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Reschedule["r1"] {
		t.Fatalf("r1 should recover first, got %+v", res.Reschedule)
	}
	if res.Reschedule["r2"] {
		t.Fatalf("r2 should be blocked by recovered r1, got %+v", res.Reschedule)
	}
	r2, _ := s.Permit("r2")
	if r2.Status != permit.StatusPendingReschedule {
		t.Fatalf("r2 pending expected, got %s", r2.Status)
	}
}

// 走廊维度的顺序敏感：r1 先恢复占掉走廊名额，r2 顺延失败。
func TestEarlierRecoveryBlocksLaterCorridor(t *testing.T) {
	n := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 4, Corridor: "C1"},
	}}
	s, _ := permit.NewService(n, map[string]int{"C1": 1})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r1", Segment: "A", Lanes: 1, Start: 10, End: 12})
	mustApply(t, s, permit.ApplyRequest{OpAt: 2, ID: "r2", Segment: "A", Lanes: 1, Start: 12, End: 13})
	res := s.Apply(permit.ApplyRequest{OpAt: 5, ID: "e", Segment: "A", Lanes: 4,
		Start: 11, End: 14, Priority: permit.Emergency})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !res.Reschedule["r1"] || res.Reschedule["r2"] {
		t.Fatalf("order-sensitive corridor result wrong: %+v", res.Reschedule)
	}
}

// 延期审查把自身原时段排除；延长段违规被拒不改变原许可；缩短无需审查。
func TestExtendExcludesSelfAndRejectionKeepsOriginal(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	// p 在 A 占 3 车道（全封闭）[10,20)。
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p", Segment: "A", Lanes: 3, Start: 10, End: 20})
	// 延到 [10,30)：自身原时段须排除，否则会与自己车道数冲突；
	// 但绕行线 B 上 [20,30) 若已有封闭则应拒绝（绕行冲突）。
	mustApply(t, s, permit.ApplyRequest{OpAt: 2, ID: "b", Segment: "B", Lanes: 1, Start: 20, End: 25})
	err := s.Extend(permit.ExtendRequest{OpAt: 3, ID: "p", NewEnd: 30})
	if err == nil || err.Code != permit.ErrDetour {
		t.Fatalf("expected detour conflict on extension, got %+v", err)
	}
	p, _ := s.Permit("p")
	if p.Current != (permit.Interval{Start: 10, End: 20}) {
		t.Fatalf("rejected extension must not change permit: %+v", p.Current)
	}
	// 缩短无需审查直接生效。
	if err := s.Extend(permit.ExtendRequest{OpAt: 4, ID: "p", NewEnd: 15}); err != nil {
		t.Fatalf("shorten should be allowed: %v", err)
	}
	p, _ = s.Permit("p")
	if p.Current.End != 15 {
		t.Fatalf("shorten not applied: %+v", p.Current)
	}
	// 已结束的许可不可延期。
	if err := s.Extend(permit.ExtendRequest{OpAt: 20, ID: "p", NewEnd: 30}); err == nil ||
		err.Code != permit.ErrPermitEnded {
		t.Fatalf("expected permit ended, got %+v", err)
	}
}

// 撤销后先前被它挡下的申请不会自动通过，须重新提交。
func TestRevokeDoesNotAutoAdmit(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "p", Segment: "A", Lanes: 3, Start: 10, End: 20})
	blocked := s.Apply(permit.ApplyRequest{OpAt: 2, ID: "q", Segment: "A", Lanes: 1, Start: 10, End: 20})
	if blocked.Err == nil || blocked.Err.Code != permit.ErrSameSegment {
		t.Fatalf("q should be blocked: %+v", blocked.Err)
	}
	if err := s.Revoke(permit.RevokeRequest{OpAt: 3, ID: "p"}); err != nil {
		t.Fatal(err)
	}
	// q 从未落档，撤销不会让它自动获批。
	if _, ok := s.Permit("q"); ok {
		t.Fatalf("q must not exist until resubmitted")
	}
	// 重新提交后通过。
	mustApply(t, s, permit.ApplyRequest{OpAt: 4, ID: "q", Segment: "A", Lanes: 1, Start: 10, End: 20})
}

// 三类冲突同时成立时只报同路段冲突。
func TestThreeConflictsReportsSameSegmentFirst(t *testing.T) {
	n, _ := testNetwork()
	// 走廊上限设为 3：same(A)+onB1(B)+onB2(B) 三份现存常规许可并发恰等于 3；
	// 候选 full(A) 进入后将同时触发：同路段（1+3>3）、绕行（B 在 A 绕行线上）、
	// 走廊（3+1>3）三类冲突，应只报同路段冲突。
	s, _ := permit.NewService(n, map[string]int{"C1": 3, "C2": 3})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "same", Segment: "A", Lanes: 1, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "onB1", Segment: "B", Lanes: 1, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "onB2", Segment: "B", Lanes: 1, Start: 10, End: 20})
	// 候选：A 全封闭（绕行冲突 + 同路段冲突），走廊此时 A、B1、B2 并发将为 3 > 2。
	res := s.Apply(permit.ApplyRequest{OpAt: 1, ID: "full", Segment: "A", Lanes: 3, Start: 10, End: 20})
	if res.Err == nil || res.Err.Code != permit.ErrSameSegment {
		t.Fatalf("must report same-segment, got %+v", res.Err)
	}
}

// 时钟回退、起始早于当前、车道超限、路段/许可不存在等错误次序。
func TestErrorPrecedenceAndClock(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 10, ID: "p", Segment: "A", Lanes: 1, Start: 10, End: 20})
	// 时钟回退优先于一切。
	res := s.Apply(permit.ApplyRequest{OpAt: 9, ID: "x", Segment: "A", Lanes: 1, Start: 1, End: 2,
		Priority: permit.Regular})
	if res.Err == nil || res.Err.Code != permit.ErrClockRollback {
		t.Fatalf("clock rollback expected, got %+v", res.Err)
	}
	// 参数非法优先于时钟（OpAt 更早但 ID 为空）。
	res = s.Apply(permit.ApplyRequest{OpAt: 1, ID: "", Segment: "A", Lanes: 1, Start: 1, End: 2})
	if res.Err == nil || res.Err.Code != permit.ErrInvalidParam {
		t.Fatalf("invalid param expected, got %+v", res.Err)
	}
	// 路段不存在。
	res = s.Apply(permit.ApplyRequest{OpAt: 10, ID: "z", Segment: "ZZ", Lanes: 1, Start: 10, End: 20})
	if res.Err == nil || res.Err.Code != permit.ErrSegmentNotFound {
		t.Fatalf("segment not found expected, got %+v", res.Err)
	}
	// 车道超限优先于同路段冲突。
	res = s.Apply(permit.ApplyRequest{OpAt: 10, ID: "z", Segment: "A", Lanes: 99, Start: 10, End: 20})
	if res.Err == nil || res.Err.Code != permit.ErrLanesExceed {
		t.Fatalf("lanes exceed expected, got %+v", res.Err)
	}
	// 起始时刻早于当前（无其他冲突时才报）。
	res = s.Apply(permit.ApplyRequest{OpAt: 10, ID: "z", Segment: "C", Lanes: 1, Start: 5, End: 8})
	if res.Err == nil || res.Err.Code != permit.ErrStartBeforeNow {
		t.Fatalf("start before now expected, got %+v", res.Err)
	}
	// 被拒绝操作不推进时钟：OpAt=10 之后仍可用 10。
	if err := s.Revoke(permit.RevokeRequest{OpAt: 10, ID: "p"}); err != nil {
		t.Fatalf("clock should remain at 10: %v", err)
	}
}

// 应急许可之间仍须满足同路段车道数约束。
func TestEmergencyVsEmergencyLaneLimit(t *testing.T) {
	n, capm := testNetwork()
	s, _ := permit.NewService(n, capm)
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "e1", Segment: "A", Lanes: 2,
		Start: 10, End: 20, Priority: permit.Emergency})
	res := s.Apply(permit.ApplyRequest{OpAt: 2, ID: "e2", Segment: "A", Lanes: 2,
		Start: 12, End: 18, Priority: permit.Emergency})
	if res.Err == nil || res.Err.Code != permit.ErrSameSegment {
		t.Fatalf("emergency-emergency must obey lane limit, got %+v", res.Err)
	}
}

// 被抢占状态（待重排）的许可可以被撤销。
func TestRevokePreemptedPermit(t *testing.T) {
	n := permit.Network{Segments: []permit.Segment{
		{ID: "A", Lanes: 4, Corridor: "C1"},
		{ID: "B", Lanes: 4, Corridor: "C1"},
		{ID: "D", Lanes: 4, Corridor: "C1"},
	}}
	// 走廊 C1 上限 2。r 在 A [15,25)；fixB、fixD 在 r 原时段之后、彼此相邻，
	// 现存集合走廊峰值恒为 1。r 被应急 [10,20) 抢占后顺延为 [20,30)，
	// 在 [26,27) 与 fixB、fixD 三者并发 = 3 > 2，顺延重审失败转待重排。
	s, _ := permit.NewService(n, map[string]int{"C1": 2})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "r", Segment: "A", Lanes: 2, Start: 15, End: 25})
	mustApply(t, s, permit.ApplyRequest{OpAt: 2, ID: "fixB", Segment: "B", Lanes: 1, Start: 25, End: 28})
	mustApply(t, s, permit.ApplyRequest{OpAt: 3, ID: "fixD", Segment: "D", Lanes: 1, Start: 26, End: 29})
	res := s.Apply(permit.ApplyRequest{OpAt: 5, ID: "e", Segment: "A", Lanes: 4,
		Start: 10, End: 20, Priority: permit.Emergency})
	if res.Err != nil || res.Reschedule["r"] {
		t.Fatalf("r should fail deferred review due to corridor cap: %+v", res)
	}
	r, _ := s.Permit("r")
	if r.Status != permit.StatusPendingReschedule {
		t.Fatalf("r should be pending reschedule, got %s", r.Status)
	}
	if err := s.Revoke(permit.RevokeRequest{OpAt: 6, ID: "r"}); err != nil {
		t.Fatalf("pending-reschedule permit should be revocable: %v", err)
	}
	r, _ = s.Permit("r")
	if r.Status != permit.StatusRevoked {
		t.Fatalf("r should be revoked, got %s", r.Status)
	}
}
