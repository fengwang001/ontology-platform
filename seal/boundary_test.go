package seal

import (
	"strconv"
	"testing"
)

func appName(i int) string { return "APP" + strconv.Itoa(i) }

// 审批档位阈值恰等：999->1人，1000->2人，9999->2人，10000->3人（含保管人）。
func TestApprovalTiers(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	mustOK(t, s.Submit(200, "A999", "E", "S", "M1", 999), "submit 999")
	mustFail(t, s.Execute(200, "A999"), ErrStateNotAllowed, "execute before approval")
	mustOK(t, s.Approve(200, "A999", "A1"), "approve 999 once")
	v, _ := s.GetApp("A999")
	if v.Status != AppApproved || v.ValidUntil != 300 {
		t.Fatalf("999 should be approved until 300, got %+v", v)
	}
	mustFail(t, s.Approve(200, "A999", "E"), ErrStateNotAllowed, "self approval")

	mustOK(t, s.Submit(201, "A1000", "E", "S", "M1", 1000), "submit 1000")
	mustOK(t, s.Approve(201, "A1000", "A1"), "first of two")
	if v, _ := s.GetApp("A1000"); v.Status != AppPending {
		t.Fatalf("1000 still pending after 1/2, got %s", v.Status)
	}
	mustFail(t, s.Approve(201, "A1000", "A1"), ErrStateNotAllowed, "same approver twice")
	mustOK(t, s.Approve(202, "A1000", "A2"), "second of two")
	if v, _ := s.GetApp("A1000"); v.Status != AppApproved {
		t.Fatalf("1000 approved after 2 distinct, got %s", v.Status)
	}

	mustOK(t, s.Submit(203, "A9999", "E", "S", "M1", 9999), "submit 9999")
	mustOK(t, s.Approve(203, "A9999", "A1"), "9999 #1")
	mustOK(t, s.Approve(203, "A9999", "A2"), "9999 #2")
	if v, _ := s.GetApp("A9999"); v.Status != AppApproved {
		t.Fatalf("9999 is tier 2, got %s", v.Status)
	}

	mustOK(t, s.Submit(204, "A10000", "E", "S", "M1", 10000), "submit 10000")
	mustOK(t, s.Approve(204, "A10000", "A1"), "10000 #1")
	mustOK(t, s.Approve(204, "A10000", "A2"), "10000 #2")
	mustFail(t, s.Approve(204, "A10000", "A3"), ErrStateNotAllowed, "final non-custodian")
	mustOK(t, s.Approve(205, "A10000", "C1"), "custodian completes tier 3")
	if v, _ := s.GetApp("A10000"); v.Status != AppApproved {
		t.Fatalf("10000 needs custodian, got %s", v.Status)
	}

	mustOK(t, s.Submit(206, "AREJ", "E", "S", "M1", 999), "submit reject-case")
	mustOK(t, s.Reject(206, "AREJ", "A9"), "reject")
	mustFail(t, s.Approve(206, "AREJ", "A1"), ErrStateNotAllowed, "approve after reject")
	dumpLogs(t, log)
}

// 有效期末刻恰等：now==ValidUntil 可执行，+1 不可。
func TestValidityDeadlineEquality(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	mustOK(t, s.Submit(200, "P", "E", "S", "M1", 999), "submit P")
	mustOK(t, s.Approve(200, "P", "A1"), "approve P")
	mustOK(t, s.Execute(300, "P"), "execute exactly at ValidUntil")
	mustOK(t, s.RegisterReceipt(300, "P"), "receipt P")

	mustOK(t, s.Submit(400, "Q", "E", "S", "M1", 999), "submit Q")
	mustOK(t, s.Approve(400, "Q", "A1"), "approve Q")
	mustFail(t, s.Execute(501, "Q"), ErrStateNotAllowed, "one second past ValidUntil")
	if v, _ := s.GetApp("Q"); v.Status != AppApproved {
		t.Fatalf("failed execution must not change state, got %s", v.Status)
	}
	dumpLogs(t, log)
}

// 授权起止两端：[ValidFrom, ValidUntil)。
func TestGrantWindowEndpoints(t *testing.T) {
	s, log := newTestService(t)
	mustOK(t, s.CreateSeal(0, "S", "contract", "C1", "C2"), "seal")
	mustOK(t, s.GrantAuthorization(0, Grant{
		ID: "G", Employee: "E", SealID: "S",
		Materials: map[string]struct{}{"M1": {}}, AmountCap: 20000, DailyCap: 10,
		ValidFrom: 100, ValidUntil: 100000,
	}), "grant [100,100000)")

	mustFail(t, s.Submit(99, "EARLY", "E", "S", "M1", 999), ErrNoGrant, "before ValidFrom")
	mustOK(t, s.Submit(100, "ATSTART", "E", "S", "M1", 999), "at ValidFrom")

	mustOK(t, s.Submit(99900, "LAST", "E", "S", "M1", 999), "submit LAST")
	mustOK(t, s.Approve(99900, "LAST", "A1"), "approve LAST")
	mustOK(t, s.Execute(99999, "LAST"), "execute at ValidUntil-1")
	mustOK(t, s.RegisterReceipt(99999, "LAST"), "receipt LAST")

	mustOK(t, s.Submit(99950, "END", "E", "S", "M1", 999), "submit END")
	mustOK(t, s.Approve(99950, "END", "A1"), "approve END")
	mustFail(t, s.Execute(100000, "END"), ErrNoGrant, "execute at grant ValidUntil")

	mustOK(t, s.Submit(99960, "REV", "E", "S", "M1", 999), "submit REV")
	mustOK(t, s.Approve(99960, "REV", "A1"), "approve REV")
	mustOK(t, s.RevokeGrant(99961, "G"), "revoke grant")
	mustFail(t, s.Execute(99970, "REV"), ErrNoGrant, "revoked grant at execution")
	dumpLogs(t, log)
}

// 日界前后一秒。
func TestDayBoundary(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	// Prepare both applications before either executes, so timestamps stay monotone.
	mustOK(t, s.Submit(86350, "D1", "E", "S", "M1", 999), "submit D1")
	mustOK(t, s.Approve(86350, "D1", "A1"), "approve D1")
	mustOK(t, s.Execute(86399, "D1"), "execute one second before boundary")
	mustOK(t, s.RegisterReceipt(86399, "D1"), "receipt D1")

	mustOK(t, s.Submit(86360, "D2", "E", "S", "M1", 999), "submit D2")
	mustOK(t, s.Approve(86360, "D2", "A1"), "approve D2")
	mustOK(t, s.Execute(86400, "D2"), "execute exactly at boundary")
	mustOK(t, s.RegisterReceipt(86400, "D2"), "receipt D2")

	if got := s.DailyUsed("G", 0); got != 1 {
		t.Fatalf("day 0 count = %d, want 1", got)
	}
	if got := s.DailyUsed("G", 1); got != 1 {
		t.Fatalf("day 1 count = %d, want 1", got)
	}
	dumpLogs(t, log)
}

// 每日上限恰等，且拒绝不增计数、跨日恢复。
func TestDailyCapEquality(t *testing.T) {
	s, log := newTestService(t)
	setupWorld(t, s)

	for i, at := range []int64{200, 300, 400} {
		app := appName(i)
		mustOK(t, s.Submit(at-10, app, "E", "S", "M1", 999), "submit "+app)
		mustOK(t, s.Approve(at-10, app, "A1"), "approve "+app)
		mustOK(t, s.Execute(at, app), "execute "+app)
		mustOK(t, s.RegisterReceipt(at, app), "receipt "+app)
	}
	if got := s.DailyUsed("G", 0); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}

	mustOK(t, s.Submit(86350, "FOURTH", "E", "S", "M1", 999), "submit FOURTH")
	mustOK(t, s.Approve(86350, "FOURTH", "A1"), "approve FOURTH")
	mustFail(t, s.Execute(86399, "FOURTH"), ErrDailyLimit, "fourth use at cap")
	if got := s.DailyUsed("G", 0); got != 3 {
		t.Fatalf("rejected execution must not count, got %d", got)
	}
	mustOK(t, s.Execute(86400, "FOURTH"), "retry next day")
	mustOK(t, s.RegisterReceipt(86400, "FOURTH"), "receipt FOURTH")
	if got := s.DailyUsed("G", 1); got != 1 {
		t.Fatalf("day 1 count = %d, want 1", got)
	}
	dumpLogs(t, log)
}
