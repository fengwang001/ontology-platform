package delivery

import "testing"

func testParams() Params {
	return Params{
		MinWaitSeconds:       100,
		MinContacts:          3,
		MinContactInterval:   10,
		CorrectionWindow:     60,
		MaxCorrectionDist:    10,
		EvidenceValidSeconds: 30,
		RiderCompensation:    50,
		MerchantConfirmWin:   20,
	}
}

func mustSys(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(testParams())
	if err != nil {
		t.Fatalf("new system: %v", err)
	}
	return s
}

func mkOrder(t *testing.T, s *System, id string, disp Disposition) {
	t.Helper()
	if err := s.PlaceOrder(id, disp, 0, 0, 0); err != nil {
		t.Fatalf("place: %v", err)
	}
	if err := s.PickUp(id, 0); err != nil {
		t.Fatalf("pickup: %v", err)
	}
}

func reportUnreachable(t *testing.T, s *System, oid string, at int64) string {
	t.Helper()
	id, err := s.ReportException(oid, at, ETUnreachable, "", 0)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	return id
}

func reportWrong(t *testing.T, s *System, oid string, at int64) string {
	t.Helper()
	id, err := s.ReportException(oid, at, ETWrongAddress, "", 0)
	if err != nil {
		t.Fatalf("report wrong: %v", err)
	}
	return id
}

func TestInvalidParams(t *testing.T) {
	p := testParams()
	p.CorrectionWindow = 0
	if _, err := NewSystem(p); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("want invalid param, got %v", err)
	}
}

func TestContactIntervalAndWaitBoundaries(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o1", DispLocal)
	ex := reportUnreachable(t, s, "o1", 100)

	if err := s.RecordContact(ex, 110); err != nil { // 首次联络不受间隔限制
		t.Fatalf("first contact: %v", err)
	}
	if err := s.RecordContact(ex, 120); err != nil { // 间隔恰等允许
		t.Fatalf("interval exact: %v", err)
	}
	if err := s.RecordContact(ex, 129); CodeOf(err) != ErrContactTooSoon {
		t.Fatalf("too soon: %v", err)
	}
	if err := s.RecordContact(ex, 130); err != nil {
		t.Fatalf("third contact: %v", err)
	}
	if err := s.JudgeUndeliverable(ex, 199); CodeOf(err) != ErrConditionWait {
		t.Fatalf("want wait, got %v", err)
	}
	if err := s.JudgeUndeliverable(ex, 200); err != nil { // 等待恰等、联络恰等
		t.Fatalf("judge exact: %v", err)
	}
	o, _ := s.GetOrder("o1", 200)
	if o.Status != OSHandled || !o.CompPaid || o.CompAmount != 50 || o.Responsibility != PartyUser {
		t.Fatalf("unexpected order: %+v", o)
	}
}

func TestConditionContactMissing(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	ex := reportUnreachable(t, s, "o", 0)
	if err := s.RecordContact(ex, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordContact(ex, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.JudgeUndeliverable(ex, 100); CodeOf(err) != ErrConditionContact {
		t.Fatalf("want contact, got %v", err)
	}
}

func TestConditionBothMissingReportsWait(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	ex := reportUnreachable(t, s, "o", 0)
	if err := s.JudgeUndeliverable(ex, 10); CodeOf(err) != ErrConditionWait {
		t.Fatalf("want wait precedence, got %v", err)
	}
}

func TestRespondThenRestartFromZero(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	ex1 := reportUnreachable(t, s, "o", 0)
	if err := s.RecordContact(ex1, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.UserRespond(ex1, 5); err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetException(ex1, 5)
	if old.Status != ESClosedUser || old.ContactCount != 1 {
		t.Fatalf("old exc: %+v", old)
	}
	o, _ := s.GetOrder("o", 5)
	if o.Status != OSPicked {
		t.Fatalf("order back to delivering: %+v", o)
	}
	ex2 := reportUnreachable(t, s, "o", 10)
	if err := s.JudgeUndeliverable(ex2, 110); CodeOf(err) != ErrConditionContact {
		t.Fatalf("new exc must start from zero, got %v", err)
	}
}

func TestRespondWinsOverJudgeAtSameInstant(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	ex := reportUnreachable(t, s, "o", 0)
	for _, at := range []int64{0, 10, 20} {
		if err := s.RecordContact(ex, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UserRespond(ex, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.JudgeUndeliverable(ex, 100); CodeOf(err) != ErrExceptionClosed {
		t.Fatalf("judge after respond must be rejected, got %v", err)
	}
}

func TestCorrectionDistanceWindowAndAutoConvert(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispReturn)
	ex := reportWrong(t, s, "o", 0)
	ev, _ := s.GetException(ex, 0)
	if ev.Deadline != 60 {
		t.Fatalf("deadline: %d", ev.Deadline)
	}
	if err := s.SubmitCorrection(ex, 11, 0, 10); CodeOf(err) != ErrDistanceExceeded {
		t.Fatalf("distance: %v", err)
	}
	o, _ := s.GetOrder("o", 10)
	if o.Status != OSException || o.AddressChanges != 0 {
		t.Fatalf("order should stay pending: %+v", o)
	}
	if err := s.SubmitCorrection(ex, 10, 0, 59); err != nil { // 距离恰等、窗口内
		t.Fatalf("exact distance: %v", err)
	}
	o, _ = s.GetOrder("o", 59)
	if o.Status != OSPicked || o.AddressX != 10 || o.AddressChanges != 1 {
		t.Fatalf("corrected order: %+v", o)
	}
	if err := s.SubmitCorrection(ex, 0, 0, 59); CodeOf(err) != ErrExceptionClosed {
		t.Fatalf("closed: %v", err)
	}

	ex2 := reportWrong(t, s, "o", 60)
	if err := s.SubmitCorrection(ex2, 0, 0, 119); err != nil { // 窗口右前一刻仍允许
		t.Fatalf("just before edge: %v", err)
	}
	ex3 := reportWrong(t, s, "o", 120)
	if err := s.SubmitCorrection(ex3, 0, 0, 180); CodeOf(err) != ErrCorrectionWindow {
		t.Fatalf("right edge: %v", err)
	}
	o, _ = s.GetOrder("o", 180) // ex3 在 180 到期转为无法送达
	if o.Status != OSReturning || o.Responsibility != PartyUser || !o.CompPaid {
		t.Fatalf("auto convert: %+v", o)
	}
}

func rejectAt(s *System, oid string, at int64, ev string, evAt int64) error {
	_, err := s.ReportException(oid, at, ETRejection, ev, evAt)
	return err
}

func TestEvidenceBoundaryAndRejectionTerminal(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	// 先接受一次时刻 70 的合法操作基线；无效上报（时刻 100、证据时刻 69）被拒，时钟不推进。
	ex := reportUnreachable(t, s, "o", 70)
	if err := s.UserRespond(ex, 75); err != nil {
		t.Fatal(err)
	}
	if err := rejectAt(s, "o", 100, "ph", 69); CodeOf(err) != ErrInvalidEvidence {
		t.Fatalf("expired evidence: %v", err)
	}
	// 被拒绝操作不推进时钟：仍可在时刻 76（恰等证据边界 76-30=46）上报。
	if err := rejectAt(s, "o", 76, "ev1", 46); err != nil {
		t.Fatalf("boundary evidence: %v", err)
	}
	o, _ := s.GetOrder("o", 76)
	if o.Status != OSHandled {
		t.Fatalf("rejection => handled, got %+v", o)
	}
	if _, err := s.ReportException("o", 80, ETUnreachable, "", 0); CodeOf(err) != ErrOrderTerminal {
		t.Fatalf("terminal report: %v", err)
	}
}

func TestReturnConfirmWindowEdges(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispReturn)
	if err := rejectAt(s, "o", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RiderReturn("o", 10); err != nil {
		t.Fatal(err)
	}
	o, _ := s.GetOrder("o", 10)
	if o.ReturnedAt != 10 || o.ConfirmDeadline != 30 {
		t.Fatalf("return: %+v", o)
	}
	// 窗口内（右端点 30 之前）确认成立，终态已退回。
	if err := s.MerchantConfirm("o", 29); err != nil {
		t.Fatalf("confirm inside: %v", err)
	}
	o, _ = s.GetOrder("o", 29)
	if o.Status != OSReturned || o.Responsibility != PartyUser {
		t.Fatalf("returned: %+v", o)
	}

	// 另一单验证右端点确认不允许。
	s2 := mustSys(t)
	mkOrder(t, s2, "o2", DispReturn)
	if err := rejectAt(s2, "o2", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if err := s2.RiderReturn("o2", 10); err != nil {
		t.Fatal(err)
	}
	if err := s2.MerchantConfirm("o2", 30); CodeOf(err) != ErrConfirmWindow {
		t.Fatalf("right edge confirm: %v", err)
	}
}

func TestReturnUnconfirmedMerchantLiable(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispReturn)
	if err := rejectAt(s, "o", 0, "ev", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RiderReturn("o", 0); err != nil {
		t.Fatal(err)
	}
	o, _ := s.GetOrder("o", 20)
	if o.Status != OSReturnUnconfirmed || o.Responsibility != PartyMerchant {
		t.Fatalf("unconfirmed: %+v", o)
	}
	if !o.CompPaid || o.CompAmount != 50 {
		t.Fatalf("rider still compensated: %+v", o)
	}
	if err := s.MerchantConfirm("o", 21); CodeOf(err) != ErrConfirmWindow {
		t.Fatalf("late confirm: %v", err)
	}
	if err := s.RiderReturn("o", 21); CodeOf(err) != ErrConfirmWindow {
		t.Fatalf("late return: %v", err)
	}
}

func TestPresetTerminalDifference(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "loc", DispLocal)
	mkOrder(t, s, "ret", DispReturn)
	if err := rejectAt(s, "loc", 0, "e1", 0); err != nil {
		t.Fatal(err)
	}
	if err := rejectAt(s, "ret", 0, "e2", 0); err != nil {
		t.Fatal(err)
	}
	lo, _ := s.GetOrder("loc", 0)
	re, _ := s.GetOrder("ret", 0)
	if lo.Status != OSHandled || re.Status != OSReturning {
		t.Fatalf("preset difference: loc=%v ret=%v", lo.Status, re.Status)
	}
}

func TestPrerequisitesAndClockRollback(t *testing.T) {
	s := mustSys(t)
	if err := s.PlaceOrder("fresh", DispLocal, 0, 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportException("fresh", 10, ETUnreachable, "", 0); CodeOf(err) != ErrNotPickedUp {
		t.Fatalf("not picked: %v", err)
	}
	if err := s.PickUp("fresh", 10); err != nil {
		t.Fatal(err)
	}
	ex := reportUnreachable(t, s, "fresh", 10)
	if _, err := s.ReportException("fresh", 11, ETWrongAddress, "", 0); CodeOf(err) != ErrActiveException {
		t.Fatalf("active exists: %v", err)
	}
	// 被拒绝操作不推进时钟：最大已接受时刻仍为 10，时刻 10 的联络必须成功。
	if err := s.RecordContact(ex, 10); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	// 时刻 9 早于已接受最大时刻 10 => 时钟回退。
	if err := s.RecordContact(ex, 9); CodeOf(err) != ErrClockRollback {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := s.ReportException("missing", 10, ETUnreachable, "", 0); CodeOf(err) != ErrOrderNotFound {
		t.Fatalf("order missing: %v", err)
	}
	if err := s.RecordContact("no-such-exc", 10); CodeOf(err) != ErrExceptionNotFound {
		t.Fatalf("exc missing: %v", err)
	}
	// 未取货/已送达语义：取货后正常送达，再上报报已送达。
	if err := s.UserRespond(ex, 11); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmDelivery("fresh", 12); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportException("fresh", 13, ETUnreachable, "", 0); CodeOf(err) != ErrAlreadyDelivered {
		t.Fatalf("delivered: %v", err)
	}
}

func TestTypeMismatch(t *testing.T) {
	s := mustSys(t)
	mkOrder(t, s, "o", DispLocal)
	ex := reportWrong(t, s, "o", 0)
	if err := s.RecordContact(ex, 0); CodeOf(err) != ErrTypeMismatch {
		t.Fatalf("contact on wrong-address: %v", err)
	}
	if err := s.JudgeUndeliverable(ex, 0); CodeOf(err) != ErrTypeMismatch {
		t.Fatalf("judge wrong-address: %v", err)
	}
	if err := s.UserRespond(ex, 0); CodeOf(err) != ErrTypeMismatch {
		t.Fatalf("respond wrong-address: %v", err)
	}
}
