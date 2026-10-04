package rma

import (
	"errors"
	"testing"
)

func mkLine(id LineID, shipped, paid int64) Line {
	return Line{ID: id, Shipped: shipped, Paid: paid}
}

func manyLines(n int) []Line {
	ls := make([]Line, n)
	for i := range ls {
		ls[i] = mkLine(LineID(i+1), 1, 1)
	}
	return ls
}

func settleOK(int64, bool, OrderID, LineID, int64, int64) (int64, int64, int64) {
	return 0, 0, 0
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name     string
		window   int64
		validFor int64
		wantErr  error
	}{
		{"ok", 30, 10, nil},
		{"window zero", 0, 10, ErrInvalid},
		{"window too big", 1_000_001, 10, ErrInvalid},
		{"valid zero", 30, 0, ErrInvalid},
		{"valid too big", 30, 1_000_001, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.window, tc.validFor)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAddOrderRejectOrder(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O1", 0, []Line{mkLine(1, 3, 1000)}, 0)
	_ = r.AddOrder("OB", 0, []Line{mkLine(1, 1, 1)}, 5)
	cases := []struct {
		name    string
		order   OrderID
		shipAt  int64
		lines   []Line
		now     int64
		wantErr error
	}{
		{"empty order", "", 0, []Line{mkLine(1, 3, 1000)}, 1, ErrInvalid},
		{"no lines", "O2", 0, nil, 1, ErrInvalid},
		{"too many lines", "O2", 0, manyLines(101), 1, ErrInvalid},
		{"dup lines", "O2", 0, []Line{mkLine(1, 1, 1), mkLine(1, 2, 2)}, 1, ErrInvalid},
		{"bad shipped", "O2", 0, []Line{mkLine(1, 0, 1)}, 1, ErrInvalid},
		{"shipped too big", "O2", 0, []Line{mkLine(1, 1_000_001, 1)}, 1, ErrInvalid},
		{"bad paid", "O2", 0, []Line{mkLine(1, 1, -1)}, 1, ErrInvalid},
		{"paid too big", "O2", 0, []Line{mkLine(1, 1, 1_000_000_000_001)}, 1, ErrInvalid},
		{"bad now", "O2", 0, []Line{mkLine(1, 1, 1)}, -1, ErrInvalid},
		{"clock back", "OC", 0, []Line{mkLine(1, 1, 1)}, 4, ErrClock},
		{"dup order", "O1", 0, []Line{mkLine(1, 3, 1000)}, 6, ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.AddOrder(tc.order, tc.shipAt, tc.lines, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAuthorizeWindowBoundary(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	if _, err := r.Authorize("REQ", "O", []Item{{Line: 1, Qty: 3}}, 30); err != nil {
		t.Fatalf("boundary now-shipAt==Wd should allow: %v", err)
	}
	r2, _ := New(30, 10)
	_ = r2.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	if _, err := r2.Authorize("REQ", "O", []Item{{1, 3}}, 31); !errors.Is(err, ErrWindow) {
		t.Fatalf("Wd+1: err=%v want ErrWindow", err)
	}
}

func TestAuthorizeCapacityAndExpiry(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	exp, err := r.Authorize("R1", "O", []Item{{1, 2}}, 5)
	if err != nil || exp != 15 {
		t.Fatalf("R1 exp=%d err=%v, want 15", exp, err)
	}
	_, err = r.Authorize("R2", "O", []Item{{1, 2}}, 6)
	var ie *ItemError
	if !errors.As(err, &ie) || ie.Index != 0 || !errors.Is(ie.Err, ErrCapacity) {
		t.Fatalf("R2 err=%v, want ItemError{0,Capacity}", err)
	}
	// t=15 恰等 exp：R1 失效，未收 2 件全释放。
	if _, err := r.Authorize("R3", "O", []Item{{1, 3}}, 15); err != nil {
		t.Fatalf("after expiry 3 free: %v", err)
	}
}

func TestExpiryReleasesOnlyUnreceived(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	_, _ = r.Authorize("R1", "O", []Item{{1, 2}}, 5)
	if _, _, _, err := r.Receive("R1", 1, 1, 10, true, settleOK); err != nil {
		t.Fatal(err)
	}
	// 收货 1 合格后余量 3-1-1=1。
	if _, err := r.Authorize("R2", "O", []Item{{1, 2}}, 11); err == nil {
		t.Fatal("only 1 free before expiry")
	}
	// 到期只释放未收 1：合格 1 不恢复，故余量恰为 1。
	if _, err := r.Authorize("R3", "O", []Item{{1, 1}}, 15); err != nil {
		t.Fatalf("after expiry 1 free: %v", err)
	}
	if _, err := r.Authorize("R4", "O", []Item{{1, 2}}, 16); err == nil {
		t.Fatal("qualified piece must not be restored: only 1 free")
	}
}

func TestReceiveRejectOrder(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000), mkLine(2, 1, 1)}, 0)
	_, _ = r.Authorize("R1", "O", []Item{{1, 1}}, 5)
	mustNotSettle := func(int64, bool, OrderID, LineID, int64, int64) (int64, int64, int64) {
		t.Fatal("settle must not run on rejection")
		return 0, 0, 0
	}
	cases := []struct {
		name    string
		id      RMAID
		line    LineID
		qty     int64
		now     int64
		wantErr error
	}{
		{"invalid qty", "R1", 1, 0, 7, ErrInvalid},
		{"missing rma", "NOPE", 1, 1, 7, ErrNotFound},
		{"line not in rma", "R1", 2, 1, 7, ErrNotFound},
		{"expired at exp", "R1", 1, 1, 15, ErrExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := r.Receive(tc.id, tc.line, tc.qty, tc.now, true, mustNotSettle)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
	if _, _, _, err := r.Receive("R1", 1, 2, 7, true, mustNotSettle); !errors.Is(err, ErrCapacity) {
		t.Fatalf("over receive: %v", err)
	}
	if _, _, _, err := r.Receive("R1", 1, 1, 3, true, mustNotSettle); !errors.Is(err, ErrClock) {
		t.Fatalf("clock back: %v", err)
	}
}

func TestRejectedOpLeavesNoTrace(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	_, _ = r.Authorize("R1", "O", []Item{{1, 1}}, 5) // exp 15
	// t=20 的超量请求会试落地 R1 到期，随后拒绝并回滚。
	_, _ = r.Authorize("BIG", "O", []Item{{1, 99}}, 20)
	if r.lastNow != 5 {
		t.Fatalf("lastNow=%d, rejected op must not advance clock", r.lastNow)
	}
	called := false
	_, _, _, err := r.Receive("R1", 1, 1, 14, true,
		func(int64, bool, OrderID, LineID, int64, int64) (int64, int64, int64) {
			called = true
			return 0, 0, 0
		})
	if err != nil || !called {
		t.Fatalf("R1 must survive rollback: err=%v called=%v", err, called)
	}
}

func TestRejectPriorities(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 3, 1000)}, 0)
	_, _ = r.Authorize("DUP", "O", []Item{{1, 1}}, 5)
	if _, err := r.Authorize("X", "O", nil, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid before clock: %v", err)
	}
	if _, err := r.Authorize("X", "GHOST", []Item{{1, 1}}, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("clock before missing order: %v", err)
	}
	if _, err := r.Authorize("DUP", "GHOST", []Item{{1, 1}}, 6); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order before conflict: %v", err)
	}
	if _, err := r.Authorize("DUP", "O", []Item{{1, 1}}, 31); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict before window: %v", err)
	}
	if _, err := r.Authorize("X", "O", []Item{{1, 99}}, 31); !errors.Is(err, ErrWindow) {
		t.Fatalf("window before capacity: %v", err)
	}
}

func TestBatchFailureMinIndexNoTrace(t *testing.T) {
	r, _ := New(30, 10)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 1, 1), mkLine(2, 1, 1), mkLine(3, 1, 1)}, 0)
	_, _ = r.Authorize("HOLD", "O", []Item{{2, 1}}, 5)
	_, err := r.Authorize("BATCH", "O", []Item{{1, 1}, {2, 2}, {3, 99}}, 6)
	var ie *ItemError
	if !errors.As(err, &ie) || ie.Index != 1 {
		t.Fatalf("err=%v, want min failing index 1", err)
	}
	if _, err := r.Authorize("BATCH", "O", []Item{{1, 1}}, 7); err != nil {
		t.Fatalf("failed batch leaves trace: %v", err)
	}
}

func TestInvariantUnderMixedGrades(t *testing.T) {
	r, _ := New(30, 100)
	_ = r.AddOrder("O", 0, []Line{mkLine(1, 10, 100000)}, 0)
	_, _ = r.Authorize("R", "O", []Item{{1, 10}}, 5)
	// 4A + 3C：C 不占余量，未收剩余 3；合格 4。
	for i := 0; i < 4; i++ {
		if _, _, _, err := r.Receive("R", 1, 1, 6, true, settleOK); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, _, _, err := r.Receive("R", 1, 1, 7, false, settleOK); err != nil {
			t.Fatal(err)
		}
	}
	om := r.orders["O"]
	ln := om.lines[1]
	if ln.qualified != 4 || ln.reserved != 3 || ln.qualified+ln.reserved != 7 {
		t.Fatalf("qualified=%d reserved=%d, want 4/3", ln.qualified, ln.reserved)
	}
	// C 释放的 3 件可立即重新授权。
	if _, err := r.Authorize("R2", "O", []Item{{1, 3}}, 8); err != nil {
		t.Fatalf("C frees capacity: %v", err)
	}
}

func TestTouchedBound(t *testing.T) {
	for _, active := range []int{100, 10_000} {
		r, _ := New(30, 10)
		_ = r.AddOrder("O", 0, []Line{mkLine(1, int64(active)+100, 0)}, 0)
		// 3 张早建授权 t=1, exp=11；active 张晚 1 单位建立 t=2, exp=12。
		// V 固定 + 时钟单调 ⇒ 早建者先到期，到期次序即建立次序。
		for i := 0; i < 3; i++ {
			_, _ = r.Authorize(RMAID("E"+string(rune('0'+i))), "O", []Item{{1, 1}}, 1)
		}
		// active 张有效授权，本次操作时不应被触碰。
		for i := 0; i < active; i++ {
			_, _ = r.Authorize(RMAID("A"+itoa(i)), "O", []Item{{1, 1}}, 2)
		}
		r.ResetTouched()
		// now=11 恰等早建单 exp：3 张到期落地，active 张全部存活；
		// 释放 3 件后新建 1 张。触碰 = 3 + 1，与 active(100/10000) 无关。
		_, err := r.Authorize("NEW", "O", []Item{{1, 1}}, 11)
		if err != nil {
			t.Fatalf("active=%d: %v", active, err)
		}
		got := r.Touched()
		if got != 4 {
			t.Fatalf("active=%d touched=%d, want 4 (3 expired + 1 new)", active, got)
		}
		if got > 3+1+1 {
			t.Fatalf("active=%d touched=%d exceeds expired+items+1 bound", active, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
