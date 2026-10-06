package seal

import "testing"

// 双人确认：非保管人、同一人重复、两人齐备。
func TestDualPresence(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	mustOK(t, s.Submit(200, "HI", "E", "S", "M1", 5000), "submit at dual threshold")
	mustOK(t, s.Approve(200, "HI", "A1"), "HI #1")
	mustOK(t, s.Approve(200, "HI", "A2"), "HI #2")

	mustFail(t, s.ConfirmPresence(200, "HI", "A1"), ErrInvalidParameter, "non-custodian confirms")
	mustOK(t, s.ConfirmPresence(200, "HI", "C1"), "C1 confirms")
	mustFail(t, s.ConfirmPresence(201, "HI", "C1"), ErrStateNotAllowed, "same custodian twice")
	mustFail(t, s.Execute(202, "HI"), ErrMissingPresence, "execute with one custodian")
	mustOK(t, s.ConfirmPresence(203, "HI", "C2"), "C2 confirms")
	mustOK(t, s.Execute(204, "HI"), "execute with two custodians")

	mustOK(t, s.Submit(300, "LO", "E", "S", "M1", 999), "submit low amount")
	mustOK(t, s.Approve(300, "LO", "A1"), "approve LO")
	mustFail(t, s.ConfirmPresence(300, "LO", "C1"), ErrStateNotAllowed, "confirm below threshold")
	mustOK(t, s.Execute(301, "LO"), "low amount executes without presence")
	dumpLogs(t, log)
}

// 核销截止恰等与多份逾期全部补登才解冻。
func TestReceiptDeadlineAndMultiFreeze(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	// app R: executed at 1000 -> deadline 1200; registering at exactly 1200 is on time.
	mustOK(t, s.Submit(900, "R", "E", "S", "M1", 999), "submit R")
	mustOK(t, s.Approve(900, "R", "A1"), "approve R")
	mustOK(t, s.Execute(1000, "R"), "execute R")
	mustOK(t, s.RegisterReceipt(1200, "R"), "receipt exactly at deadline")
	if s.IsFrozen(1201, "E") {
		t.Fatal("on-time receipt must not freeze")
	}

	// two more executed apps, both receipts overdue.
	for _, id := range []string{"O1", "O2"} {
		at := int64(2000)
		if id == "O2" {
			at = 2100
		}
		mustOK(t, s.Submit(at-50, id, "E", "S", "M1", 999), "submit "+id)
		mustOK(t, s.Approve(at-50, id, "A1"), "approve "+id)
		mustOK(t, s.Execute(at, id), "execute "+id)
	}
	// deadlines: O1=2200, O2=2300.
	if s.IsFrozen(2200, "E") {
		t.Fatal("at deadline == now, not overdue yet")
	}
	if !s.IsFrozen(2201, "E") {
		t.Fatal("one second after earliest deadline -> frozen")
	}
	// frozen: new applications refused and existing approved apps unexecutable.
	mustOK(t, s.Submit(2150, "WAIT", "E", "S", "M1", 999), "submit WAIT before freeze")
	mustOK(t, s.Approve(2150, "WAIT", "A1"), "approve WAIT")
	mustFail(t, s.Submit(2201, "NEW", "E", "S", "M1", 999), ErrFrozen, "submit while frozen")
	mustFail(t, s.Execute(2250, "WAIT"), ErrFrozen, "execute approved app while frozen")

	// clear O1 only -> O2 still overdue, freeze persists.
	mustOK(t, s.RegisterReceipt(2250, "O1"), "late receipt O1")
	if !s.IsFrozen(2301, "E") {
		t.Fatal("one overdue remaining -> still frozen")
	}
	// clear O2 -> freeze lifts; the earlier rejected WAIT is NOT revived by thaw,
	// but it remains APPROVED and becomes executable again while its window lasts.
	mustOK(t, s.RegisterReceipt(2301, "O2"), "late receipt O2")
	if s.IsFrozen(2301, "E") {
		t.Fatal("all overruns cleared -> unfrozen")
	}
	// WAIT was approved at 2150 -> valid until 2250; thaw at 2301 is too late,
	// proving thaw is non-retroactive.
	mustFail(t, s.Execute(2302, "WAIT"), ErrStateNotAllowed, "thaw does not revive expired window")
	dumpLogs(t, log)
}

// 停用后恢复：停用立刻杀死未执行申请，恢复不复活。
func TestSealDisableEnable(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	mustOK(t, s.Submit(200, "P", "E", "S", "M1", 999), "submit P")
	mustOK(t, s.Approve(200, "P", "A1"), "approve P")
	mustOK(t, s.Submit(200, "U", "E", "S", "M1", 999), "submit U")

	mustOK(t, s.SetSealStatus(210, "S", SealDisabled, "audit"), "disable")
	if v, _ := s.GetApp("P"); v.Status != AppDead {
		t.Fatalf("approved un-executed app must die, got %s", v.Status)
	}
	if v, _ := s.GetApp("U"); v.Status != AppDead {
		t.Fatalf("pending app must die, got %s", v.Status)
	}
	mustFail(t, s.Execute(211, "P"), ErrStateNotAllowed, "execute dead app")
	mustFail(t, s.Submit(212, "N", "E", "S", "M1", 999), ErrStateNotAllowed, "submit on disabled seal")

	mustOK(t, s.SetSealStatus(220, "S", SealNormal, "audit done"), "re-enable")
	if v, _ := s.GetApp("P"); v.Status != AppDead {
		t.Fatalf("dead apps must not auto-revive, got %s", v.Status)
	}
	mustOK(t, s.Submit(221, "FRESH", "E", "S", "M1", 999), "fresh application after restore")

	// executed uses survive disablement and can still be receipted/voided.
	mustOK(t, s.Approve(221, "FRESH", "A1"), "approve FRESH")
	mustOK(t, s.Execute(222, "FRESH"), "execute FRESH")
	mustOK(t, s.SetSealStatus(223, "S", SealDisabled, "again"), "disable again")
	mustOK(t, s.RegisterReceipt(224, "FRESH"), "receipt survives disable")
	mustFail(t, s.MarkVoid(225, "FRESH", "A1"), ErrInvalidParameter, "non-custodian void")
	mustOK(t, s.MarkVoid(226, "FRESH", "C1"), "custodian void")
	if got := s.DailyUsed("G", 0); got != 1 {
		t.Fatalf("void must not refund daily count, got %d", got)
	}
	dumpLogs(t, log)
}

// 时钟回退：仅在被接受的操作之后检查；被拒操作不推进时钟。
func TestClockMonotonic(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	mustOK(t, s.Submit(500, "P", "E", "S", "M1", 999), "submit at 500")
	mustFail(t, s.Approve(499, "P", "A1"), ErrClockBackward, "backward call rejected")
	// a rejected call at a high time must NOT move the clock.
	mustFail(t, s.Submit(999, "MISSING-SEAL-REF", "E", "NOPE", "M1", 999), ErrNotFound, "fails for other reason")
	mustFail(t, s.Approve(501, "P", "A1"), ErrClockBackward, "clock still 500")
	mustOK(t, s.Approve(500, "P", "A1"), "equal now is fine")
	if s.LastAcceptedNow() != 500 {
		t.Fatalf("last now = %d, want 500", s.LastAcceptedNow())
	}
	dumpLogs(t, log)
}

// 拒绝优先级：参数 > 时钟 > 不存在 > 状态 > 冻结 > 授权 > 限额 > 日限 > 在场。
func TestErrorPriority(t *testing.T) {
	s, _ := newTestService(t)
	setupWorld(t, s)

	// invalid parameter beats clock backward.
	mustFail(t, s.Execute(-5, ""), ErrInvalidParameter, "parameter beats clock")

	// clock backward beats missing entity (last accepted time is 0 from setup;
	// advance the clock first).
	mustOK(t, s.Submit(1000, "P", "E", "S", "M1", 999), "advance clock")
	mustFail(t, s.Execute(900, "GHOST"), ErrClockBackward, "clock beats not-found")

	// not-found beats state issues.
	mustFail(t, s.Execute(1000, "GHOST"), ErrNotFound, "not-found at equal time")

	// frozen beats grant expiry: make E frozen with an overdue app, then revoke
	// the grant and try to execute an approved app -> FROZEN first.
	mustOK(t, s.Approve(1000, "P", "A1"), "approve P")
	mustOK(t, s.Execute(1001, "P"), "execute P") // deadline 1201
	mustOK(t, s.Submit(1002, "Q", "E", "S", "M1", 999), "submit Q")
	mustOK(t, s.Approve(1002, "Q", "A1"), "approve Q")
	mustOK(t, s.RevokeGrant(1003, "G"), "revoke G")
	mustFail(t, s.Execute(1202, "Q"), ErrFrozen, "frozen beats revoked grant")

	// missing presence is the lowest priority: a dual-threshold app with a
	// missing confirmation hits presence only after every higher check passes.
	s2, log2 := newTestService(t)
	setupWorld(t, s2)
	mustOK(t, s2.Submit(200, "D", "E", "S", "BAD", 500000), "submit D: bad material & huge amount")
	mustOK(t, s2.Approve(200, "D", "A1"), "D #1")
	mustOK(t, s2.Approve(200, "D", "A2"), "D #2")
	mustOK(t, s2.Approve(200, "D", "C1"), "D #3 custodian")
	// no presence confirmation; amount cap is exceeded -> LIMIT beats MISSING_PRESENCE.
	mustFail(t, s2.Execute(201, "D"), ErrLimit, "limit beats missing presence")
	dumpLogs(t, log2)
}
