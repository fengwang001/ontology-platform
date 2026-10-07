package enrollment

import (
	"errors"
	"testing"
)

// testCalendar builds n terms [10i, 10i+10) with the deadline at 10i+5.
func testCalendar(n int) *Calendar {
	terms := make([]Term, n)
	for i := range terms {
		terms[i] = Term{Index: i, Start: int64(10 * i), End: int64(10*i + 10), SubmitDeadline: int64(10*i + 5)}
	}
	cal, err := NewCalendar(terms)
	if err != nil {
		panic(err)
	}
	return cal
}

func testConfig() Config {
	return Config{
		Levels: map[AppType]int{
			AppSuspend: 1, AppResume: 1, AppTransfer: 2, AppReserve: 1, AppWithdraw: 1,
		},
		SuspendCap:  4,
		ReserveCap:  2,
		MaxYears:    4,
		AppDeadline: 100,
	}
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(testCalendar(10), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddMajor("CS", 5); err != nil {
		t.Fatal(err)
	}
	if err := e.AddMajor("EE", 1); err != nil {
		t.Fatal(err)
	}
	return e
}

func codeOf(err error) ErrCode {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return -1
}

func mustApprove(t *testing.T, e *Engine, sid, who string, at int64, reject bool, want ErrCode) {
	t.Helper()
	err := e.Decide(DecisionInput{StudentID: sid, Approver: who, At: at, Reject: reject})
	if (want == 0 && err != nil) || (want != 0 && codeOf(err) != want) {
		t.Fatalf("decide sid=%s who=%s reject=%v at=%d: got %v want code %d", sid, who, reject, at, err, want)
	}
}

func mustSnap(t *testing.T, e *Engine, sid string, tick int64, st Status, major string, open bool) {
	t.Helper()
	sn, err := e.SnapshotAt(sid, tick)
	if err != nil {
		t.Fatalf("snapshot %s @%d: %v", sid, tick, err)
	}
	if sn.Status != st || sn.Major != major || sn.OpenApp != open {
		t.Fatalf("snapshot %s @%d = %+v, want status=%s major=%s open=%v", sid, tick, sn, st, major, open)
	}
}

func TestDeadlineEquality(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s1", "CS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s1", Type: AppTransfer, At: 15, Submitter: "u", Major: "EE"}); err != nil {
		t.Fatalf("submit at deadline: %v", err)
	}
	mustApprove(t, e, "s1", "a", 15, false, 0)
	mustApprove(t, e, "s1", "b", 15, false, 0)
	mustSnap(t, e, "s1", 15, StatusEnrolled, "EE", false)

	if err := e.Admit("s2", "CS", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s2", Type: AppTransfer, At: 26, Submitter: "u", Major: "EE"}); codeOf(err) != ErrDeadlinePassed {
		t.Fatalf("transfer after deadline: got %v", err)
	}
	id2, err := e.Submit(SubmitInput{StudentID: "s2", Type: AppSuspend, At: 26, Submitter: "u", Terms: 1})
	if err != nil {
		t.Fatalf("suspend after deadline: %v", err)
	}
	mustApprove(t, e, "s2", "a", 26, false, 0)
	mustSnap(t, e, "s2", 29, StatusEnrolled, "CS", false)
	mustSnap(t, e, "s2", 30, StatusSuspended, "CS", false)
	if id2 == "" {
		t.Fatal("empty app id")
	}
}

func TestSuspendCapEquality(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 2}); err != nil {
		t.Fatalf("suspend 2: %v", err)
	}
	mustApprove(t, e, "s", "a", 1, false, 0)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 21, Submitter: "u"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	mustApprove(t, e, "s", "a", 21, false, 0)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 31, Submitter: "u", Terms: 2}); err != nil {
		t.Fatalf("suspend exactly cap 4: %v", err)
	}
	mustApprove(t, e, "s", "a", 31, false, 0)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 41, Submitter: "u"}); err != nil {
		t.Fatalf("resume 2: %v", err)
	}
	mustApprove(t, e, "s", "a", 41, false, 0)
	_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 51, Submitter: "u", Terms: 1})
	if codeOf(err) != ErrLimitExceeded {
		t.Fatalf("suspend 5th term: got %v want limit", err)
	}
}

func TestYearExhaustionLazyWithdraw(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 0); err != nil {
		t.Fatal(err)
	}
	// Enrolled terms 0..3, then suspend for term 4 (suspension consumes no
	// enrolled-year terms). Submitted at the term-4 deadline, effective term 4.
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 45, Submitter: "u", Terms: 1}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	mustApprove(t, e, "s", "a", 45, false, 0)
	mustSnap(t, e, "s", 50, StatusSuspended, "CS", false)
	// Resume effective term 5: enrolled terms spent are 0..3 = 4 == MaxYears,
	// so the resume is rejected and withdrawal lands lazily on the next touch.
	_, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 55, Submitter: "u"})
	if codeOf(err) != ErrLimitExceeded {
		t.Fatalf("resume exhausted: got %v want limit", err)
	}
	// Still reserved immediately after the rejection; the withdrawal lands on
	// the next mutating touch.
	mustSnap(t, e, "s", 55, StatusSuspended, "CS", false)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 56, Submitter: "u"}); codeOf(err) != ErrTerminalState {
		t.Fatalf("next touch should land withdraw first: got %v", err)
	}
	mustSnap(t, e, "s", 50, StatusWithdrawn, "CS", false)
	mustSnap(t, e, "s", 49, StatusSuspended, "CS", false)
	mustSnap(t, e, "s", 40, StatusEnrolled, "CS", false)
	_, err = e.Submit(SubmitInput{StudentID: "s", Type: AppWithdraw, At: 57, Submitter: "u"})
	if codeOf(err) != ErrTerminalState {
		t.Fatalf("after lazy withdraw: got %v want terminal", err)
	}
}

func TestApplicationDeadlineEquality(t *testing.T) {
	cfg := testConfig()
	cfg.AppDeadline = 5
	e, err := NewEngine(testCalendar(10), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddMajor("CS", 5); err != nil {
		t.Fatal(err)
	}
	if err := e.Admit("s", "CS", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppSuspend, At: 1, Submitter: "u", Terms: 1}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "a", 6, false, 0)
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 11, Submitter: "u"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Decide(DecisionInput{StudentID: "s", Approver: "a", At: 17}); codeOf(err) != ErrDeadlinePassed {
		t.Fatalf("expired decide: got %v", err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppResume, At: 18, Submitter: "u"}); err != nil {
		t.Fatalf("resubmit after expiry: %v", err)
	}
}

func TestMultiLevelReject(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppTransfer, At: 11, Submitter: "u", Major: "EE"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "a", 11, false, 0)
	mustApprove(t, e, "s", "b", 12, true, 0)
	mustSnap(t, e, "s", 12, StatusEnrolled, "CS", false)
	if err := e.Decide(DecisionInput{StudentID: "s", Approver: "c", At: 12}); codeOf(err) != ErrNotFound {
		t.Fatalf("decide closed: got %v", err)
	}
	if e.majors["EE"].quota != 1 {
		t.Fatalf("ee quota = %d want 1", e.majors["EE"].quota)
	}
}

func TestDuplicateApprover(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppTransfer, At: 11, Submitter: "u", Major: "EE"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "a", 11, false, 0)
	if err := e.Decide(DecisionInput{StudentID: "s", Approver: "a", At: 12}); codeOf(err) != ErrNoPermission {
		t.Fatalf("same approver two levels: got %v", err)
	}
	if err := e.Decide(DecisionInput{StudentID: "s", Approver: "u", At: 12}); codeOf(err) != ErrNoPermission {
		t.Fatalf("self approve: got %v", err)
	}
}

func TestQuotaThenAddThenApprove(t *testing.T) {
	e := newTestEngine(t)
	if err := e.Admit("s", "CS", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s", Type: AppTransfer, At: 11, Submitter: "u", Major: "EE"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "a", 11, false, 0)
	if err := e.Admit("s2", "CS", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(SubmitInput{StudentID: "s2", Type: AppTransfer, At: 21, Submitter: "u2", Major: "EE"}); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s2", "a2", 21, false, 0)
	mustApprove(t, e, "s2", "b2", 21, false, 0)
	mustSnap(t, e, "s2", 21, StatusEnrolled, "EE", false)
	if err := e.Decide(DecisionInput{StudentID: "s", Approver: "b", At: 22}); codeOf(err) != ErrQuotaInsufficient {
		t.Fatalf("no quota: got %v", err)
	}
	mustSnap(t, e, "s", 22, StatusEnrolled, "CS", true)
	if err := e.AddQuota("EE", 1, 23); err != nil {
		t.Fatal(err)
	}
	mustApprove(t, e, "s", "b", 24, false, 0)
	mustSnap(t, e, "s", 24, StatusEnrolled, "EE", false)
}
