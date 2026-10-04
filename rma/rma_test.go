package rma

import (
	"errors"
	"fmt"
	"testing"
)

func mustStore(t *testing.T, wd, v int64) *Store {
	t.Helper()
	s, err := New(Config{WindowDays: wd, ValidFor: v})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNewInvalid(t *testing.T) {
	for _, c := range []Config{{0, 10}, {30, 0}, {1_000_001, 10}, {30, 1_000_001}} {
		if _, err := New(c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("New(%+v) err=%v", c, err)
		}
	}
}

func TestWindowBoundary(t *testing.T) {
	s := mustStore(t, 30, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 5, Paid: 100}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("R0", "O1", []Item{{"L1", 1}}, 30); err != nil {
		t.Fatalf("t=30 (equal Wd) should pass: %v", err)
	}
	if _, err := s.Authorize("R1", "O1", []Item{{"L1", 1}}, 31); !errors.Is(err, ErrWindow) {
		t.Fatalf("t=31 err=%v want ErrWindow", err)
	}
}

func TestClockBackwardAndRejectNoAdvance(t *testing.T) {
	s := mustStore(t, 30, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 5, Paid: 100}}, 5); err != nil {
		t.Fatal(err)
	}
	// t=9 的非法操作被拒，时钟不推进；t=8 仍应被接受。
	if err := s.AddOrder("O2", 0, nil, 9); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil lines err=%v", err)
	}
	if err := s.AddOrder("O2", 0, map[string]Line{"L1": {Shipped: 1, Paid: 1}}, 8); err != nil {
		t.Fatalf("t=8 after rejected t=9: %v", err)
	}
	if err := s.AddOrder("O3", 0, map[string]Line{"L1": {Shipped: 1, Paid: 1}}, 7); !errors.Is(err, ErrClockBack) {
		t.Fatalf("t=7 err=%v want ErrClockBack", err)
	}
}

func TestAddOrderConflictAndValidation(t *testing.T) {
	s := mustStore(t, 30, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 5, Paid: 100}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 5, Paid: 100}}, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup order err=%v want ErrConflict", err)
	}
	bad := []struct {
		id     string
		shipAt int64
		lines  map[string]Line
	}{
		{"", 0, map[string]Line{"L": {1, 0}}},
		{"O", -1, map[string]Line{"L": {1, 0}}},
		{"O", 0, nil},
		{"O", 0, map[string]Line{}},
		{"O", 0, map[string]Line{"L": {0, 0}}},
		{"O", 0, map[string]Line{"L": {1_000_001, 0}}},
		{"O", 0, map[string]Line{"L": {1, -1}}},
	}
	for i, b := range bad {
		if err := s.AddOrder(b.id, b.shipAt, b.lines, 2); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad case %d err=%v want ErrInvalid", i, err)
		}
	}
}

func TestExpireExactAndReleasesOnlyOpen(t *testing.T) {
	// V=10：now==exp 即失效；失效只释放未收部分。
	s := mustStore(t, 30, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 3, Paid: 1000}}, 0); err != nil {
		t.Fatal(err)
	}
	exp, err := s.Authorize("R1", "O1", []Item{{"L1", 2}}, 5)
	if err != nil || exp != 15 {
		t.Fatalf("auth exp=%d err=%v", exp, err)
	}
	if info, _ := s.InspectLine("O1", "L1"); info.Reserved != 2 {
		t.Fatalf("reserved=%d want 2", info.Reserved)
	}
	// t=14 未到期，余量 1，申请 2 件被拒且不留痕。
	if _, err := s.Authorize("R2", "O1", []Item{{"L1", 2}}, 14); !errors.Is(err, ErrCapacity) {
		t.Fatalf("t=14 err=%v want ErrCapacity", err)
	}
	if info, _ := s.InspectLine("O1", "L1"); info.Reserved != 2 {
		t.Fatalf("reject changed reserved=%d", info.Reserved)
	}
	// t=15 恰等 exp：R1 失效，未收 2 件全释放，R2 申 2 件成功。
	if _, err := s.Authorize("R2", "O1", []Item{{"L1", 2}}, 15); err != nil {
		t.Fatalf("t=15 R2: %v", err)
	}
	if a, _ := s.InspectAuth("R1"); a.Valid {
		t.Fatalf("R1 must be invalid at t==exp")
	}
	if info, _ := s.InspectLine("O1", "L1"); info.Reserved != 2 {
		t.Fatalf("reserved after expire+new=%d want 2", info.Reserved)
	}
}

func TestBatchAllOrNothing(t *testing.T) {
	s := mustStore(t, 30, 100)
	if err := s.AddOrder("O1", 0, map[string]Line{
		"L1": {Shipped: 2, Paid: 10},
		"L2": {Shipped: 1, Paid: 10},
		"L3": {Shipped: 1, Paid: 10},
	}, 0); err != nil {
		t.Fatal(err)
	}
	// 第三项超余量 → 整单拒绝，前两项也不预占，单号不留痕。
	_, err := s.Authorize("RX", "O1",
		[]Item{{"L1", 2}, {"L2", 1}, {"", 2}}, 0)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty line err=%v", err)
	}
	_, err = s.Authorize("RX", "O1",
		[]Item{{"L1", 2}, {"L2", 1}, {"L3", 2}}, 0)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("err=%v want ErrCapacity", err)
	}
	for _, ln := range []string{"L1", "L2", "L3"} {
		if info, _ := s.InspectLine("O1", ln); info.Reserved != 0 {
			t.Fatalf("line %s reserved=%d after reject", ln, info.Reserved)
		}
	}
	if _, ok := s.InspectAuth("RX"); ok {
		t.Fatalf("RX must not exist after reject")
	}
	if _, err := s.Authorize("RX", "O1", []Item{{"L1", 1}}, 0); err != nil {
		t.Fatalf("reuse RX after reject: %v", err)
	}
}

func TestAuthorizeRejectTable(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		order   string
		items   []Item
		now     int64
		seed    bool // 是否预置冲突授权单
		smallWd bool // 使用 Wd=1 制造窗口外
		wantErr error
	}{
		{"bad now", "R", "O1", []Item{{"L1", 1}}, -1, false, false, ErrInvalid},
		{"empty id", "", "O1", []Item{{"L1", 1}}, 0, false, false, ErrInvalid},
		{"dup line", "R", "O1", []Item{{"L1", 1}, {"L1", 1}}, 0, false, false, ErrInvalid},
		{"qty zero", "R", "O1", []Item{{"L1", 0}}, 0, false, false, ErrInvalid},
		{"101 items", "R", "O1", make([]Item, 101), 0, false, false, ErrInvalid},
		{"order missing", "R", "NOPE", []Item{{"L1", 1}}, 0, false, false, ErrNotFound},
		{"line missing", "R", "O1", []Item{{"XX", 1}}, 0, false, false, ErrNotFound},
		{"dup auth id", "R1", "O1", []Item{{"L1", 1}}, 1, true, false, ErrConflict},
		{"outside window", "R2", "O1", []Item{{"L1", 1}}, 9, false, true, ErrWindow},
		{"capacity", "R3", "O1", []Item{{"L1", 6}}, 0, false, false, ErrCapacity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wd := int64(30)
			if tc.smallWd {
				wd = 1
			}
			s := mustStore(t, wd, 10)
			if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 5, Paid: 1}}, 0); err != nil {
				t.Fatal(err)
			}
			if tc.seed {
				if _, err := s.Authorize("R1", "O1", []Item{{"L1", 1}}, 1); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.Authorize(tc.id, tc.order, tc.items, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRejectPrecedence(t *testing.T) {
	s := mustStore(t, 100, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 1, Paid: 1}}, 5); err != nil {
		t.Fatal(err)
	}
	// 时钟回退先于不存在。
	if _, err := s.Authorize("R", "GHOST", []Item{{"X", 1}}, 4); !errors.Is(err, ErrClockBack) {
		t.Fatalf("got %v want ErrClockBack", err)
	}
	// 行不存在先于单号冲突（同一次操作同时满足两者）。
	if _, err := s.Authorize("DUP", "O1", []Item{{"GHOST", 1}}, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if _, err := s.Authorize("DUP", "O1", []Item{{"L1", 1}}, 5); err != nil {
		t.Fatal(err)
	}
	// 冲突先于窗口：余量充足的订单上，冲突单即使超时也报冲突。
	if err := s.AddOrder("O2", 0, map[string]Line{"L1": {Shipped: 100, Paid: 1}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("DUP2", "O2", []Item{{"L1", 1}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("DUP", "O1", []Item{{"L1", 1}}, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v want ErrConflict", err)
	}
	if _, err := s.Authorize("DUP2", "O2", []Item{{"L1", 2}}, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v want ErrConflict", err)
	}
	// 窗口先于余量：新单号、窗口外（200>100）且超余量，报窗口。
	if _, err := s.Authorize("R2", "O2", []Item{{"L1", 99}}, 200); !errors.Is(err, ErrWindow) {
		t.Fatalf("got %v want ErrWindow", err)
	}
}

func TestCapacityInvariant(t *testing.T) {
	s := mustStore(t, 30, 10)
	if err := s.AddOrder("O1", 0, map[string]Line{"L1": {Shipped: 4, Paid: 100}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("R1", "O1", []Item{{"L1", 3}}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("R2", "O1", []Item{{"L1", 1}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize("R3", "O1", []Item{{"L1", 1}}, 7); !errors.Is(err, ErrCapacity) {
		t.Fatalf("overbook err=%v", err)
	}
	info, _ := s.InspectLine("O1", "L1")
	if info.A+info.B+info.Reserved > info.Shipped {
		t.Fatalf("invariant broken: %+v", info)
	}
}

func TestTouchedIndependentOfActiveCount(t *testing.T) {
	var prev int
	for _, n := range []int{100, 10000} {
		s := mustStore(t, 1_000_000, 1_000_000)
		// 订单行上限 100：背景授权单铺在多个订单的不同行上，
		// 目标授权落在独立订单的全新行，确保触碰数与背景单总数无关。
		for i := 0; i < n; i++ {
			oi, li := i/50, i%50
			order := fmt.Sprintf("O%04d", oi)
			if i%50 == 0 {
				lines := map[string]Line{}
				for j := 0; j < 50; j++ {
					lines[fmt.Sprintf("L%02d", j)] = Line{Shipped: 1000, Paid: 100}
				}
				if err := s.AddOrder(order, 0, lines, 1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Authorize(fmt.Sprintf("R%05d", i), order,
				[]Item{{fmt.Sprintf("L%02d", li), 1}}, 1); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.AddOrder("OT", 0, map[string]Line{
			"T1": {Shipped: 10, Paid: 100},
			"T2": {Shipped: 10, Paid: 100},
		}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authorize("TARGET", "OT", []Item{
			{"T1", 1},
			{"T2", 1},
		}, 2); err != nil {
			t.Fatal(err)
		}
		got := s.Touched()
		if got > 2+1 {
			t.Fatalf("n=%d touched=%d > expired(0)+items(2)+1", n, got)
		}
		if s.ValidAuthCount() != n+1 {
			t.Fatalf("valid=%d want %d", s.ValidAuthCount(), n+1)
		}
		if n > 100 && got != prev {
			t.Fatalf("touched drift 100→%d, %d→%d", prev, n, got)
		}
		prev = got
	}
}

func TestTouchedExpiryBound(t *testing.T) {
	s := mustStore(t, 1_000, 10)
	lines := map[string]Line{}
	for i := 0; i < 10; i++ {
		lines[fmt.Sprintf("L%d", i)] = Line{Shipped: 1, Paid: 1}
	}
	if err := s.AddOrder("O", 0, lines, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Authorize(fmt.Sprintf("E%d", i), "O",
			[]Item{{fmt.Sprintf("L%d", i), 1}}, 0); err != nil {
			t.Fatal(err)
		}
	}
	// t=11 使 E0、E1（exp=10）与 E2 全部到期：到期 3 单 + 项 1 + 建单 1。
	if _, err := s.Authorize("X", "O", []Item{{"L3", 1}}, 11); err != nil {
		t.Fatal(err)
	}
	if got := s.Touched(); got > 3+1+1 {
		t.Fatalf("touched=%d > 3 expired + 1 item + 1", got)
	}
}
