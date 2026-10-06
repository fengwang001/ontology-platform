package staffing

import "testing"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantCode(t *testing.T, err error, c Code) {
	t.Helper()
	if ErrCode(err) != c {
		t.Fatalf("want code %s, got %v", c, err)
	}
}

func newTestSvc(t *testing.T, cooldown, grace int) *Service {
	t.Helper()
	s := New(cooldown, grace)
	must(t, s.AddPosition(0, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 2}))
	must(t, s.AddCandidate(0, "C1"))
	must(t, s.AddCandidate(0, "C2"))
	must(t, s.AddCandidate(0, "C3"))
	return s
}

// 占用恰满：两份通知后第三份拒绝 HEADCOUNT_FULL。
func TestOccupiedExactlyFull(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	_, err := s.IssueOffer(1, "C1", "P", 150, 10)
	must(t, err)
	_, err = s.IssueOffer(1, "C2", "P", 150, 10)
	must(t, err)
	occ, onb, pen, err := s.Occupancy(1, "P")
	must(t, err)
	if occ != 2 || onb != 0 || pen != 2 {
		t.Fatalf("occ=%d onb=%d pen=%d", occ, onb, pen)
	}
	_, err = s.IssueOffer(1, "C3", "P", 150, 10)
	wantCode(t, err, CodeHeadcountFull)
	snap, err := s.Snapshot(1)
	must(t, err)
	must(t, CheckInvariant(snap))
}

// 带宽两端恰等允许。
func TestBandEndpointsInclusive(t *testing.T) {
	s := New(5, 5)
	must(t, s.AddPosition(0, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 4}))
	for _, c := range []string{"L", "H", "X"} {
		must(t, s.AddCandidate(0, c))
	}
	_, err := s.IssueOffer(2, "L", "P", 100, 10)
	must(t, err)
	_, err = s.IssueOffer(2, "H", "P", 200, 10)
	must(t, err)
	_, err = s.IssueOffer(2, "X", "P", 99, 10)
	wantCode(t, err, CodeBandExceeded)
	_, err = s.IssueOffer(2, "X", "P", 201, 10)
	wantCode(t, err, CodeBandExceeded)
}

// 例外审批在季度边界：89 属 Q0，90 属 Q1；额度按季度独立。
func TestExceptionQuarterBoundary(t *testing.T) {
	if QuarterOf(89) != 0 || QuarterOf(90) != 1 || QuarterOf(179) != 1 || QuarterOf(180) != 2 {
		t.Fatal("quarter boundary wrong")
	}
	s := New(5, 5)
	must(t, s.AddPosition(0, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 10}))
	must(t, s.AddCandidate(0, "C1"))
	must(t, s.AddCandidate(0, "C2"))
	must(t, s.AddException(0, "E0", "P", 0, 1))

	_, err := s.IssueOffer(89, "C1", "P", 201, 95)
	must(t, err)
	_, err = s.IssueOffer(89, "C2", "P", 201, 95)
	wantCode(t, err, CodeBandExceeded) // Q0 额度耗尽
	_, err = s.IssueOffer(90, "C2", "P", 201, 95)
	wantCode(t, err, CodeBandExceeded) // Q1 无额度

	must(t, s.AddException(90, "E1", "P", 1, 1))
	_, err = s.IssueOffer(90, "C2", "P", 201, 95)
	must(t, err)

	snap, err := s.Snapshot(90)
	must(t, err)
	if snap.Exceptions["E0"].Remaining != 0 || snap.Exceptions["E1"].Remaining != 0 {
		t.Fatalf("remaining E0=%d E1=%d", snap.Exceptions["E0"].Remaining, snap.Exceptions["E1"].Remaining)
	}
}

// 冷却恰等天数：拒绝日 d，冷却 5；d+4 拒绝，d+5 允许。
func TestCooldownExact(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	id, err := s.IssueOffer(10, "C1", "P", 150, 20)
	must(t, err)
	must(t, s.Respond(10, id, false, -1))
	_, err = s.IssueOffer(14, "C1", "P", 150, 20)
	wantCode(t, err, CodeCooldown)
	_, err = s.IssueOffer(15, "C1", "P", 150, 20)
	must(t, err)
}

// 放弃同样触发冷却。
func TestAbandonCooldown(t *testing.T) {
	s := newTestSvc(t, 3, 2)
	id, err := s.IssueOffer(0, "C1", "P", 150, 5)
	must(t, err)
	must(t, s.Respond(0, id, true, 5))
	_, err = s.GetOffer(8, id) // 5+2=7，day 8 放弃
	must(t, err)
	_, err = s.IssueOffer(10, "C1", "P", 150, 20)
	wantCode(t, err, CodeCooldown)
	_, err = s.IssueOffer(11, "C1", "P", 150, 20)
	must(t, err)
}

// 答复截止日恰等允许，晚一日惰性过期并释放。
func TestDeadlineExactAndNextDay(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	id, err := s.IssueOffer(0, "C1", "P", 150, 10)
	must(t, err)
	id2, err := s.IssueOffer(0, "C2", "P", 150, 10)
	must(t, err)
	must(t, s.Respond(10, id, true, 12))
	o, err := s.GetOffer(10, id)
	must(t, err)
	if o.Status != StatusAccepted {
		t.Fatalf("status=%s", o.Status)
	}

	err = s.Respond(11, id2, false, -1)
	wantCode(t, err, CodeExpired)
	o2, err := s.GetOffer(11, id2)
	must(t, err)
	if o2.Status != StatusExpired {
		t.Fatalf("status=%s", o2.Status)
	}
	occ, _, _, err := s.Occupancy(11, "P")
	must(t, err)
	if occ != 1 {
		t.Fatalf("occupied=%d, want 1", occ)
	}
}

// 惰性过期在发放路径被整岗结算触发，释放后可再发；过期不触发冷却。
func TestLazyExpiryReleases(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	id, err := s.IssueOffer(0, "C1", "P", 150, 5)
	must(t, err)
	id2, err := s.IssueOffer(6, "C2", "P", 150, 10)
	must(t, err)
	if id2 == 0 {
		t.Fatal("expected re-issuance after lazy expiry")
	}
	o, err := s.GetOffer(6, id)
	must(t, err)
	if o.Status != StatusExpired {
		t.Fatalf("status=%s", o.Status)
	}
	_, err = s.IssueOffer(6, "C1", "P", 150, 10)
	must(t, err)
}

// 放弃宽限恰等：entry=5, grace=2；day 7 仍可入职，day 8 放弃。
func TestGraceExact(t *testing.T) {
	s := newTestSvc(t, 3, 2)
	id, err := s.IssueOffer(0, "C1", "P", 150, 5)
	must(t, err)
	id2, err := s.IssueOffer(0, "C2", "P", 150, 5)
	must(t, err)
	must(t, s.Respond(0, id, true, 5))
	must(t, s.Respond(0, id2, true, 5))
	must(t, s.Onboard(7, id))

	err = s.Onboard(8, id2)
	wantCode(t, err, CodeExpired)
	o2, _ := s.GetOffer(8, id2)
	if o2.Status != StatusAbandoned {
		t.Fatalf("status=%s", o2.Status)
	}
}

// 冻结下：不可新发，但已有通知仍可答复与入职。
func TestFrozenRespondOnboard(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	id, err := s.IssueOffer(0, "C1", "P", 150, 10)
	must(t, err)
	must(t, s.SetFrozen(1, "P", true))
	_, err = s.IssueOffer(1, "C2", "P", 150, 10)
	wantCode(t, err, CodeFrozen)
	must(t, s.Respond(2, id, true, 4))
	must(t, s.Onboard(4, id))
	must(t, s.SetFrozen(5, "P", false))
	_, err = s.IssueOffer(5, "C2", "P", 150, 10)
	must(t, err)
}

// 编制下调到恰等于已占用允许，再低拒绝。
func TestAdjustDownToOccupied(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	_, err := s.IssueOffer(0, "C1", "P", 150, 10)
	must(t, err)
	must(t, s.AdjustHeadcount(1, "P", 1))
	err = s.AdjustHeadcount(1, "P", 0)
	wantCode(t, err, CodeHeadcountFull)
}

// 批量整批失败不留痕：失败后无通知、无占用、例外不消耗。
func TestBatchAllOrNothing(t *testing.T) {
	s := New(5, 5)
	must(t, s.AddPosition(0, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 10}))
	for _, c := range []string{"A", "B", "C", "D"} {
		must(t, s.AddCandidate(0, c))
	}
	must(t, s.AddException(0, "E", "P", 0, 1))

	_, err := s.IssueBatch(0, "P", []BatchItem{
		{CandidateID: "A", PositionID: "P", Salary: 201, Deadline: 10},
		{CandidateID: "B", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "C", PositionID: "P", Salary: 201, Deadline: 10}, // 第 2 个超带宽，无额度
	})
	if e, ok := err.(*Error); !ok || e.Code != CodeBandExceeded || e.Index != 2 {
		t.Fatalf("want BAND_EXCEEDED at index 2, got %v", err)
	}
	snap, _ := s.Snapshot(0)
	if len(snap.Offers) != 0 || snap.Pending["P"] != 0 || snap.Occupied["P"] != 0 {
		t.Fatalf("state changed after failed batch: offers=%d occ=%d", len(snap.Offers), snap.Occupied["P"])
	}
	if snap.Exceptions["E"].Remaining != 1 {
		t.Fatalf("exception consumed on failed batch: %d", snap.Exceptions["E"].Remaining)
	}

	// 编制超限报最小失败下标 2。
	must(t, s.AdjustHeadcount(0, "P", 2))
	_, err = s.IssueBatch(0, "P", []BatchItem{
		{CandidateID: "A", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "B", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "C", PositionID: "P", Salary: 100, Deadline: 10},
	})
	if e, ok := err.(*Error); !ok || e.Code != CodeHeadcountFull || e.Index != 2 {
		t.Fatalf("want HEADCOUNT_FULL at index 2, got %v", err)
	}

	// 成功批次原子占用三份。
	must(t, s.AdjustHeadcount(0, "P", 3))
	ids, err := s.IssueBatch(0, "P", []BatchItem{
		{CandidateID: "A", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "B", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "C", PositionID: "P", Salary: 100, Deadline: 10},
	})
	must(t, err)
	if len(ids) != 3 {
		t.Fatalf("ids=%v", ids)
	}

	// 候选人重复未决（跨批次内重复）：报后一个下标（留出编制避免先满编）。
	must(t, s.AdjustHeadcount(1, "P", 5))
	_, err = s.IssueBatch(1, "P", []BatchItem{
		{CandidateID: "D", PositionID: "P", Salary: 100, Deadline: 10},
		{CandidateID: "D", PositionID: "P", Salary: 100, Deadline: 10},
	})
	if e, ok := err.(*Error); !ok || e.Code != CodePendingExists || e.Index != 1 {
		t.Fatalf("want PENDING_EXISTS index 1, got %v", err)
	}
}

// 拒绝优先级：在同一操作同时具备多种失败条件时，只报优先级最高者。
func TestErrorPriority(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	must(t, s.AddException(0, "E", "P", 0, 1))
	// 参数非法（空候选人）压过一切。
	_, err := s.IssueOffer(0, "", "P", 99999, -1)
	wantCode(t, err, CodeInvalidParam)
	// 时钟回退压过不存在/冻结/满/带宽/未决/冷却。
	_, err = s.IssueOffer(-5, "C1", "NOPE", -1, -10)
	wantCode(t, err, CodeInvalidParam) // now<0 仍是参数非法
	_, err = s.IssueOffer(-1, "C1", "P", 150, 5)
	wantCode(t, err, CodeInvalidParam)
	_, err = s.IssueOffer(5, "C1", "P", 150, 10)
	must(t, err)
	_, err = s.IssueOffer(3, "C2", "P", 150, 10)
	wantCode(t, err, CodeClockRollback)

	// 不存在（岗位）压过冻结。
	must(t, s.SetFrozen(6, "P", true))
	_, err = s.IssueOffer(6, "C2", "MISSING", 150, 10)
	wantCode(t, err, CodeNotFound)
	// 冻结压过编制满 / 带宽 / 未决 / 冷却。
	_, err = s.IssueOffer(6, "C1", "P", 150, 10)
	wantCode(t, err, CodeFrozen)
	must(t, s.SetFrozen(7, "P", false))

	// 编制已满压过带宽超限（C1 已有未决但满编优先于未决）。
	_, err = s.IssueOffer(7, "C2", "P", 150, 10) // 第二份，满编
	must(t, err)
	_, err = s.IssueOffer(7, "C3", "P", 999, 10) // 既满编又超带宽且本人冷却之外
	wantCode(t, err, CodeHeadcountFull)

	// 空编制岗位：带宽超限压过未决/冷却。
	must(t, s.AddPosition(8, PositionSpec{ID: "Q", BandLow: 100, BandHigh: 100, Headcount: 5}))
	_, err = s.IssueOffer(8, "C1", "Q", 50, 12) // C1 在 P 有未决，但 Q 上先报带宽
	wantCode(t, err, CodeBandExceeded)

	// 带宽内但已有未决：报 PENDING_EXISTS。
	_, err = s.IssueOffer(8, "C1", "Q", 100, 12)
	wantCode(t, err, CodePendingExists)
}

// 时钟回退：已接受操作后 now 不可倒退；被拒绝操作不推进时钟。
func TestClockRollback(t *testing.T) {
	s := New(5, 5)
	must(t, s.AddPosition(10, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 2}))
	must(t, s.AddCandidate(10, "C1"))
	// 被拒绝操作（超带宽）不推进时钟：下一个 now=10 仍合法。
	_, err := s.IssueOffer(15, "C1", "P", 999, 20)
	wantCode(t, err, CodeBandExceeded)
	_, err = s.IssueOffer(10, "C1", "P", 150, 20)
	must(t, err)
	_, err = s.IssueOffer(9, "C2", "P", 150, 20)
	wantCode(t, err, CodeClockRollback) // 时钟回退优先于不存在
}

// 被拒绝操作不得顺手物化他人到期通知：day 11 给 C3 发放因带宽超限被拒，
// 整岗结算中 C1 的到期通知必须一并回滚，仍保持 PENDING。
func TestRejectedOpNoSideEffect(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	idA, err := s.IssueOffer(0, "C1", "P", 150, 5) // day 11 时已到期
	must(t, err)
	_, err = s.IssueOffer(0, "C2", "P", 150, 20)
	must(t, err)
	_, err = s.IssueOffer(11, "C3", "P", 999, 15) // 带宽超限 -> 拒绝
	wantCode(t, err, CodeBandExceeded)
	got, err := s.GetOffer(0, idA) // 被拒操作未推进时钟，now=0 合法
	must(t, err)
	if got.Status != StatusPending {
		t.Fatalf("rejected op materialized expiry: status=%s", got.Status)
	}
}

// 撤回、取消、离职的占用与冷却语义。
func TestWithdrawCancelLeave(t *testing.T) {
	s := newTestSvc(t, 5, 5)
	id, err := s.IssueOffer(0, "C1", "P", 150, 10)
	must(t, err)
	// 已接受不可撤回。
	must(t, s.Respond(0, id, true, 3))
	err = s.Withdraw(1, id)
	wantCode(t, err, CodeInvalidState)
	// 取消不触发冷却。
	must(t, s.Cancel(2, id))
	_, err = s.IssueOffer(2, "C1", "P", 150, 10)
	must(t, err)

	id2, err := s.IssueOffer(3, "C2", "P", 150, 10)
	must(t, err)
	must(t, s.Respond(3, id2, true, 5))
	must(t, s.Onboard(5, id2))
	must(t, s.Leave(6, "C2"))
	occ, onb, _, err := s.Occupancy(6, "P")
	must(t, err)
	if occ != 1 || onb != 0 {
		t.Fatalf("occ=%d onb=%d", occ, onb)
	}
	err = s.Leave(6, "C2")
	wantCode(t, err, CodeInvalidState) // 不可重复离职

	// 撤回未决不触发冷却。
	id3, err := s.IssueOffer(7, "C3", "P", 150, 10)
	must(t, err)
	must(t, s.Withdraw(7, id3))
	_, err = s.IssueOffer(7, "C3", "P", 150, 10)
	must(t, err)
}
