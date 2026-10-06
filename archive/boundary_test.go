package archive

import "testing"

// 借期最后一日当天归还不算逾期；晚一日算 1 天逾期。
func TestDueDayBoundary(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	l.result("Return @10 (last day)", s.Return(10, "u0", "v0"))
	if got := s.GetUser("u0"); got.OverdueTotal != 0 || got.Status != UserActive {
		t.Fatalf("due-day return must be on time, got %+v", got)
	}
	l.result("Borrow u0 v1 @10", s.Borrow(10, "u0", "v1"))
	l.result("Return @21 (one day late)", s.Return(21, "u0", "v1"))
	if got := s.GetUser("u0"); got.OverdueTotal != 1 {
		t.Fatalf("one day late must count 1, got %d", got.OverdueTotal)
	}
	l.log("snapshot:\n%s", s.Snapshot())
}

// 续借窗口起点恰等允许；新借期自原最后一日下一日起算；续借次数上限。
func TestRenewWindowStartAndNewDue(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0 due10", s.Borrow(0, "u0", "v0"))
	l.result("Renew @4 (window not open)", s.Renew(4, "u0", "v0", ""))
	o := s.Renew(5, "u0", "v0", "")
	l.result("Renew @5 (window start exact)", o)
	if !o.OK {
		t.Fatal("window start must be allowed")
	}
	v := s.GetVolume("v0")
	if v.Loan.DueDay != 20 {
		t.Fatalf("new period starts day after old due, got %d want 20", v.Loan.DueDay)
	}
	if v.Loan.RenewalsUsed != 1 {
		t.Fatalf("renewals=%d want 1", v.Loan.RenewalsUsed)
	}
	l.result("Renew 2nd @8 (limit)", s.Renew(8, "u0", "v0", ""))
}

// 机密续借需审批：无审批人/本人审批均拒绝；有审批通过，新借期从原最后一日顺延。
func TestRenewApproval(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v2 @0 due10", s.Borrow(0, "u0", "v2"))
	l.result("Renew confidential no approver", s.Renew(5, "u0", "v2", ""))
	// 本人审批：参数非法，且先于不存在性检查（用不存在的卷也仍报参数非法）
	o := s.Renew(5, "u0", "v2", "u0")
	l.result("Renew self-approver", o)
	if o.Err != ErrInvalidParam {
		t.Fatalf("self approval must be invalid_param, got %s", o.Err)
	}
	o = s.Renew(5, "u0", "ghost", "u0")
	if o.Err != ErrInvalidParam {
		t.Fatalf("self approval precedes existence, got %s", o.Err)
	}
	o = s.Renew(5, "u0", "v2", "boss")
	l.result("Renew approved by boss", o)
	if !o.OK || s.GetVolume("v2").Loan.DueDay != 20 {
		t.Fatalf("approved renew must extend to 20, got %+v", o)
	}
}

// 存在有效预约不得续借（冻结的预约也算有效预约）。
func TestRenewBlockedByReservation(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	l.result("Reserve u1 @1", s.Reserve(1, "u1", "v0"))
	o := s.Renew(5, "u0", "v0", "")
	l.result("Renew with queue", o)
	if o.Err != ErrReservation {
		t.Fatalf("active reservation must block renew, got %s", o.Err)
	}
	l.result("Cancel u1 reservation @5", s.CancelReservation(5, "u1", "v0"))
	l.result("Renew @5 after cancel", s.Renew(5, "u0", "v0", ""))
}

// 队首密级被下调被跳过但保留位置；恢复后仍凭原序位优先。
func TestQueueHeadSkippedKeepsPosition(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v2 @0", s.Borrow(0, "u0", "v2"))
	l.result("Reserve u1 (head)", s.Reserve(0, "u1", "v2"))
	l.result("Reserve u0 (second)", s.Reserve(1, "u0", "v2"))
	if o := s.SetClassification("u1", ClassInternal); !o.OK {
		t.Fatal(o.Reason)
	}
	l.log("u1 downgraded to internal; head should be skipped but keep position")
	l.result("Return @2", s.Return(2, "u0", "v2"))
	if h := s.GetVolume("v2").Hold; h == nil || h.UserID != "u0" {
		t.Fatalf("must assign to u0, got %+v", h)
	}
	if q := s.GetVolume("v2").QueueUserIDs; len(q) != 1 || q[0] != "u1" {
		t.Fatalf("u1 must keep position, got %v", q)
	}
	l.result("Restore u1", s.SetClassification("u1", ClassTopSecret))
	l.result("Pickup u0 @3", s.Pickup(3, "u0", "v2"))
	l.result("Return u0 @4", s.Return(4, "u0", "v2"))
	if h := s.GetVolume("v2").Hold; h == nil || h.UserID != "u1" {
		t.Fatalf("u1 restored must get volume by original position, got %+v", h)
	}
}

// 取卷期限恰等可取；届满次日未取视为放弃、失去位置并继续分配。
func TestPickupDeadlineBoundary(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	l.result("Reserve u1 @0", s.Reserve(0, "u1", "v0"))
	l.result("Return @2 (deadline=5)", s.Return(2, "u0", "v0"))
	l.result("Pickup u1 @5 (exact deadline)", s.Pickup(5, "u1", "v0"))
	if s.GetVolume("v0").Loan == nil {
		t.Fatal("pickup on exact deadline day must succeed")
	}
	// 第二轮：u0 预约，分配日 6、期限 9，@10 届满放弃。
	l.result("Return u1 @6", s.Return(6, "u1", "v0"))
	// 用新卷 v1 验证届满放弃（时间不早于当前时钟 6）。
	l.result("Borrow u0 v1 @6", s.Borrow(6, "u0", "v1"))
	l.result("Reserve u1 v1 @7", s.Reserve(7, "u1", "v1"))
	l.result("Return u0 v1 @8 (u1 hold, deadline11)", s.Return(8, "u0", "v1"))
	l.result("Advance @12 (u1 forfeits v1)", s.Advance(12))
	o := s.Pickup(12, "u1", "v1")
	l.result("Pickup u1 v1 @12 (expired)", o)
	if o.OK {
		t.Fatal("pickup after deadline must fail")
	}
	if s.GetVolume("v1").Status != VolInLibrary {
		t.Fatalf("expired hold with empty queue must return to library, got %s",
			s.GetVolume("v1").Status)
	}
}

// 取卷届满放弃后继续分配给后续有效预约者。
func TestPickupExpireCascades(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	l.result("Reserve u1 @0", s.Reserve(0, "u1", "v0"))
	l.result("Reserve u0 @1", s.Reserve(1, "u0", "v0"))
	l.result("Return @2 -> u1 hold till 5", s.Return(2, "u0", "v0"))
	l.result("Advance @6 (u1 forfeits)", s.Advance(6))
	if h := s.GetVolume("v0").Hold; h == nil || h.UserID != "u0" {
		t.Fatalf("after forfeiture volume must cascade to u0, got %+v", h)
	}
	if q := s.GetVolume("v0").QueueUserIDs; len(q) != 0 {
		t.Fatalf("u1 loses position and u0 is assigned (leaves queue), queue=%v", q)
	}
}

// 累计逾期恰等阈值进入暂停；冷静期恰等解除并清零；未达冷静期不解除。
func TestSuspendThresholdAndCooldown(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow v0 @0", s.Borrow(0, "u1", "v0"))
	l.result("Borrow v1 @0", s.Borrow(0, "u1", "v1"))
	l.result("Return v0 @12 overdue2", s.Return(12, "u1", "v0"))
	if s.GetUser("u1").Status != UserActive {
		t.Fatal("total 2 < threshold 5, must stay active")
	}
	l.result("Return v1 @13 overdue3 total5", s.Return(13, "u1", "v1"))
	if s.GetUser("u1").Status != UserSuspended {
		t.Fatal("total exactly at threshold must suspend")
	}
	// 暂停期间借阅被拒
	l.result("Borrow while suspended", s.Borrow(14, "u1", "v0"))
	l.result("Advance @15 (2 days, not enough)", s.Advance(15))
	if s.GetUser("u1").Status != UserSuspended {
		t.Fatal("@15 is only 2 days after last return, must stay suspended")
	}
	l.result("Advance @16 (exact 3 days)", s.Advance(16))
	u := s.GetUser("u1")
	if u.Status != UserActive || u.OverdueTotal != 0 {
		t.Fatalf("exact cooldown must release and clear total, got %+v", u)
	}
}

// 仍在借的逾期卷归还前不计入累计；全部归还前不得解除暂停。
func TestOverdueCountedOnlyAtReturn(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow v0 @0", s.Borrow(0, "u1", "v0"))
	l.result("Borrow v1 @0", s.Borrow(0, "u1", "v1"))
	l.result("Return v0 @20 overdue10 suspend", s.Return(20, "u1", "v0"))
	if s.GetUser("u1").Status != UserSuspended {
		t.Fatal("must be suspended")
	}
	l.result("Advance @30 with loan open", s.Advance(30))
	if s.GetUser("u1").Status != UserSuspended {
		t.Fatal("open loan blocks release")
	}
	l.result("Return v1 @30", s.Return(30, "u1", "v1"))
	l.result("Advance @32 (2 days)", s.Advance(32))
	if s.GetUser("u1").Status != UserSuspended {
		t.Fatal("cooldown must restart from last return")
	}
	l.result("Advance @33 (exact 3)", s.Advance(33))
	if s.GetUser("u1").Status != UserActive {
		t.Fatal("must be released after all returned and cooldown")
	}
}

// 封存借出中的卷：仍须归还，归还后封存，预约取消且不再分配。
func TestSealAfterReturn(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	l.result("Reserve u1 @1", s.Reserve(1, "u1", "v0"))
	l.result("Seal v0 while lent", s.Seal("v0"))
	v := s.GetVolume("v0")
	if !v.SealPending || v.Status != VolLent || len(v.QueueUserIDs) != 0 {
		t.Fatalf("seal while lent must pend and clear queue, got %+v", v)
	}
	l.result("Return u0 v0 @5", s.Return(5, "u0", "v0"))
	v = s.GetVolume("v0")
	if v.Status != VolSealed || v.Hold != nil || len(v.QueueUserIDs) != 0 {
		t.Fatalf("after return must be sealed with no assignment, got %+v", v)
	}
	o := s.Borrow(6, "u1", "v0")
	l.result("Borrow sealed", o)
	if o.Err != ErrState {
		t.Fatalf("sealed volume rejects borrow with state, got %s", o.Err)
	}
}

// 批量整批失败不留痕；失败下标与错误优先级精确。
func TestBatchAllOrNothing(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow u0 v0 @0", s.Borrow(0, "u0", "v0"))
	o, idx := s.BorrowBatch(1, "u1", []string{"v1", "v0"})
	l.log("Batch[v1 ok,v0 lent] -> ok=%v err=%s idx=%d", o.OK, o.Err, idx)
	if o.OK || idx != 1 || o.Err != ErrAlreadyLent {
		t.Fatalf("whole batch rejected, fail idx=1, got ok=%v idx=%d err=%s", o.OK, idx, o.Err)
	}
	if s.GetVolume("v1").Status != VolInLibrary {
		t.Fatal("rejected batch must leave no trace on v1")
	}
	o, idx = s.BorrowBatch(1, "u1", []string{"v1", "v1"})
	l.log("Batch duplicate -> err=%s idx=%d", o.Err, idx)
	if o.Err != ErrInvalidParam || idx != -1 {
		t.Fatalf("duplicate is invalid_param with idx=-1, got %s %d", o.Err, idx)
	}
	// idx0 密级不足（优先级高） vs idx1 已借出
	o, idx = s.BorrowBatch(1, "u2", []string{"v2", "v0"})
	l.log("Batch[v2 clearance,v0 lent] -> err=%s idx=%d", o.Err, idx)
	if o.Err != ErrClearance || idx != 0 {
		t.Fatalf("clearance outranks already-lent, got %s idx=%d", o.Err, idx)
	}
	o, idx = s.BorrowBatch(2, "u1", []string{"v1", "v3"})
	l.log("Batch[v1,v3] @2 -> ok=%v idx=%d %s", o.OK, idx, o.Reason)
	if !o.OK {
		t.Fatal(o.Reason)
	}
	if s.GetVolume("v1").Loan.UserID != "u1" || s.GetVolume("v3").Loan.UserID != "u1" {
		t.Fatal("successful batch must lend all")
	}
}

// 拒绝优先级：参数非法 > 时钟回退 > 不存在 > 状态 > 密级 > 暂停 > 已借出。
func TestRejectionPriority(t *testing.T) {
	s := newServiceWith(t)
	if o := s.Borrow(5, "", "v0"); o.Err != ErrInvalidParam {
		t.Fatalf("empty id: %s", o.Err)
	}
	if o := s.Borrow(-9, "", "v0"); o.Err != ErrInvalidParam {
		t.Fatalf("invalid param outranks clock: %s", o.Err)
	}
	s.Borrow(10, "u0", "v0")
	if o := s.Borrow(9, "ghost", "ghost"); o.Err != ErrClockRollback {
		t.Fatalf("clock outranks not-found: %s", o.Err)
	}
	if o := s.Borrow(11, "u0", "nope"); o.Err != ErrNotFound {
		t.Fatalf("not-found: %s", o.Err)
	}
	s.Seal("v1")
	if o := s.Borrow(11, "u2", "v1"); o.Err != ErrState {
		t.Fatalf("sealed(state) outranks clearance: %s", o.Err)
	}
	// 密级不足 vs 暂停：构造 u2(internal) 暂停后借机密卷
	s2 := newServiceWith(t)
	s2.Borrow(0, "u2", "v1")
	s2.Return(20, "u2", "v1") // overdue 10 -> suspended
	if s2.GetUser("u2").Status != UserSuspended {
		t.Fatal("u2 must be suspended")
	}
	if o := s2.Borrow(21, "u2", "v2"); o.Err != ErrClearance {
		t.Fatalf("clearance outranks suspended: %s", o.Err)
	}
	// 暂停且密级足够 -> 暂停
	if o := s2.Borrow(21, "u2", "v0"); o.Err != ErrSuspended {
		t.Fatalf("suspended: %s", o.Err)
	}
	// 密级足够、正常但卷借出 -> 已借出
	if o := s.Borrow(11, "u1", "v0"); o.Err != ErrAlreadyLent {
		t.Fatalf("already lent: %s", o.Err)
	}
}

// 被拒绝操作不改变时钟与任何状态。
func TestRejectedOpNoSideEffect(t *testing.T) {
	s, l := newServiceWith(t), newStepLogger(t)
	l.result("Borrow @10", s.Borrow(10, "u0", "v0"))
	before := s.Snapshot()
	l.result("Rollback attempt @9", s.Borrow(9, "u1", "v1"))
	l.result("Borrow lent v0 @11", s.Borrow(11, "u1", "v0"))
	after := s.Snapshot()
	if before != after {
		t.Fatalf("rejected operations changed state:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// 时钟仍停在 10：回退操作被拒，@11 尝试虽不回退但因卷借出被拒，lastNow 仍为 10。
	if o := s.Borrow(9, "u1", "v1"); o.Err != ErrClockRollback {
		t.Fatalf("clock must still be 10, so @9 is rollback: got %s", o.Err)
	}
	l.result("Borrow v1 @10 proves clock still 10", s.Borrow(10, "u1", "v1"))
}
