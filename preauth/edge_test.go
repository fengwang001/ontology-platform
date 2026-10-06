package preauth

import (
	"errors"
	"testing"
)

func testSystem() *System {
	return NewSystem(Config{ValidityDays: 5, ToleranceBPS: 1000})
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", ctx, got, want)
	}
}

func avail(t *testing.T, s *System, acc string, now int64) int64 {
	t.Helper()
	v, err := s.Available(acc, now)
	mustOK(t, err, "available")
	return v
}

func TestExpiryBoundary(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 30, 0), "auth")
	if v := avail(t, s, "C", 0); v != 70 {
		t.Fatalf("day0 avail=%d want 70", v)
	}
	mustOK(t, s.Capture("A", 10, false, 4), "capture on expiry day")
	if v := avail(t, s, "C", 4); v != 70 {
		t.Fatalf("day4 avail=%d want 70", v)
	}
	wantErr(t, s.Capture("A", 5, false, 5), ErrClosed, "capture after expiry")
	vw, err := s.Auth("A", 5)
	mustOK(t, err, "auth view")
	if vw.Status != StatusExpired || vw.Captured != 10 || vw.Remaining != 0 {
		t.Fatalf("day5 view=%+v", vw)
	}
	if v := avail(t, s, "C", 5); v != 90 {
		t.Fatalf("day5 avail=%d want 90", v)
	}
	mustOK(t, s.Authorize("B", "C", 90, 6), "reuse after expiry")
	wantErr(t, s.Authorize("B2", "C", 1, 6), ErrInsufficient, "no credit left")
}

func TestIncrementFailureIsolation(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 80, 0), "auth")
	mustOK(t, s.Authorize("B", "C", 20, 1), "auth b")
	wantErr(t, s.Increment("A", 1, 2), ErrInsufficient, "increment too big")
	vw, _ := s.Auth("A", 2)
	if vw.Authorized != 80 || vw.Remaining != 80 || vw.ExpiresDay != 4 {
		t.Fatalf("original auth mutated: %+v", vw)
	}
	if v := avail(t, s, "C", 2); v != 0 {
		t.Fatalf("avail=%d want 0", v)
	}
	mustOK(t, s.Capture("A", 10, false, 4), "capture original on expiry day")

	mustOK(t, s.CreateAccount("C2", 100, 10), "create2")
	mustOK(t, s.Authorize("I", "C2", 60, 10), "auth i")
	mustOK(t, s.Increment("I", 30, 14), "incr last day")
	iv, _ := s.Auth("I", 14)
	if iv.Authorized != 90 || iv.ExpiresDay != 18 || iv.Remaining != 90 {
		t.Fatalf("incr view=%+v", iv)
	}
	mustOK(t, s.Capture("I", 5, false, 18), "capture on new expiry day")
	wantErr(t, s.Capture("I", 1, false, 19), ErrClosed, "expired day after")
}

func TestOvercaptureAndTolerance(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 200, 0), "create")
	mustOK(t, s.Authorize("A", "C", 100, 0), "auth")
	mustOK(t, s.Capture("A", 100, false, 1), "capture to hold zero")
	if v := avail(t, s, "C", 1); v != 100 {
		t.Fatalf("avail=%d want 100", v)
	}
	mustOK(t, s.Capture("A", 10, false, 2), "overcapture within tolerance")
	vw, _ := s.Auth("A", 2)
	if vw.Remaining != 0 || vw.Captured != 110 {
		t.Fatalf("view=%+v", vw)
	}
	if v := avail(t, s, "C", 2); v != 90 {
		t.Fatalf("avail=%d want 90", v)
	}
	wantErr(t, s.Capture("A", 1, false, 3), ErrOverTolerance, "111 exceeds 110")

	s2 := testSystem()
	mustOK(t, s2.CreateAccount("C2", 1000, 0), "create2")
	mustOK(t, s2.Authorize("G", "C2", 103, 0), "auth g")
	mustOK(t, s2.Capture("G", 113, false, 1), "floor ok")
	wantErr(t, s2.Capture("G", 1, false, 2), ErrOverTolerance, "114 exceeds")
}

func TestOvercaptureInsufficient(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 100, 0), "auth")
	mustOK(t, s.Capture("A", 100, false, 1), "capture")
	wantErr(t, s.Capture("A", 1, false, 2), ErrInsufficient, "no free credit")
	vw, _ := s.Auth("A", 2)
	if vw.Captured != 100 || vw.Remaining != 0 {
		t.Fatalf("view=%+v", vw)
	}
}

func TestFinalAndVoid(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 60, 0), "auth a")
	mustOK(t, s.Capture("A", 20, true, 1), "final capture")
	vw, _ := s.Auth("A", 1)
	if vw.Status != StatusFinalized || vw.Remaining != 0 {
		t.Fatalf("final view=%+v", vw)
	}
	if v := avail(t, s, "C", 1); v != 80 {
		t.Fatalf("avail=%d want 80", v)
	}
	wantErr(t, s.Capture("A", 1, false, 2), ErrClosed, "capture finalized")
	wantErr(t, s.Increment("A", 1, 2), ErrClosed, "increment finalized")
	wantErr(t, s.Void("A", 2), ErrClosed, "void finalized")
	mustOK(t, s.Refund("A", 20, 3), "refund finalized")
	if v := avail(t, s, "C", 3); v != 100 {
		t.Fatalf("avail=%d want 100", v)
	}

	mustOK(t, s.Authorize("V", "C", 10, 4), "auth v")
	mustOK(t, s.Void("V", 5), "void")
	vv, _ := s.Auth("V", 5)
	if vv.Status != StatusVoided || vv.Remaining != 0 {
		t.Fatalf("void view=%+v", vv)
	}
	wantErr(t, s.Capture("V", 1, false, 6), ErrClosed, "capture voided")
}

func TestRefund(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 50, 0), "auth")
	mustOK(t, s.Capture("A", 30, false, 1), "capture")
	mustOK(t, s.Refund("A", 30, 2), "refund all")
	if v := avail(t, s, "C", 2); v != 80 {
		t.Fatalf("avail=%d want 80", v)
	}
	wantErr(t, s.Refund("A", 1, 3), ErrRefundExceeds, "refund over captured")
	vw, _ := s.Auth("A", 2)
	if vw.Refunded != 30 || vw.Remaining != 20 || vw.Status != StatusActive {
		t.Fatalf("view=%+v", vw)
	}
}

func TestDuplicateAndPriorities(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 10, 0), "auth")
	mustOK(t, s.Void("A", 1), "void")
	wantErr(t, s.Authorize("A", "C", 10, 2), ErrDuplicateID, "reuse closed id")
	wantErr(t, s.Authorize("A", "C", 10, 0), ErrClockBackward, "clock before dup")
	wantErr(t, s.Authorize("", "C", 0, 0), ErrInvalid, "invalid first")
	wantErr(t, s.Authorize("X", "NOPE", 1, 3), ErrInvalid, "missing account")
	wantErr(t, s.Capture("NOID", 1, false, 3), ErrNotFound, "missing auth")
	wantErr(t, s.Refund("NOID", 1, 3), ErrNotFound, "refund missing auth")
	mustOK(t, s.Authorize("Z", "C", 10, 3), "auth z")
	mustOK(t, s.Void("Z", 4), "void z")
	wantErr(t, s.Capture("Z", 100000, false, 5), ErrClosed, "closed before tolerance/funds")
}

func TestRejectedLeavesNoTrace(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 10, 5), "auth at day5")
	wantErr(t, s.Authorize("Q", "C", 1, 3), ErrClockBackward, "backward")
	mustOK(t, s.Authorize("P", "C", 1, 5), "still day5")
	wantErr(t, s.Authorize("BIG", "C", 1000, 6), ErrInsufficient, "rejected at day6")
	mustOK(t, s.Authorize("P2", "C", 1, 5), "clock still 5")
	mustOK(t, s.Authorize("BIG", "C", 1, 7), "rejected id reusable")
}

func TestAdjustCredit(t *testing.T) {
	s := testSystem()
	mustOK(t, s.CreateAccount("C", 100, 0), "create")
	mustOK(t, s.Authorize("A", "C", 80, 0), "auth")
	mustOK(t, s.AdjustCredit("C", 150, 1), "raise")
	if v := avail(t, s, "C", 1); v != 70 {
		t.Fatalf("avail=%d want 70", v)
	}
	mustOK(t, s.AdjustCredit("C", 90, 2), "lower ok")
	wantErr(t, s.AdjustCredit("C", 79, 3), ErrInsufficient, "lower below holds")
	if v := avail(t, s, "C", 3); v != 10 {
		t.Fatalf("avail=%d want 10 after rejected adjust", v)
	}
	wantErr(t, s.AdjustCredit("NOPE", 10, 3), ErrNotFound, "adjust missing account")
}
