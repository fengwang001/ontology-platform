package hold_test

import (
	"errors"
	"testing"

	"ontology/hold"
	"ontology/payout"
	"ontology/revenue"
)

func mustLedger(t *testing.T, wd, min int64) *payout.Ledger {
	t.Helper()
	l, err := payout.New(wd, min)
	if err != nil {
		t.Fatalf("payout.New: %v", err)
	}
	return l
}

func splitOne(t *testing.T, l *payout.Ledger, now int64, content, creator string) {
	t.Helper()
	if err := l.SetSplit(now, content, []revenue.Part{{Creator: creator, BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
}

func TestHoldHalfOpenInterval(t *testing.T) {
	l := mustLedger(t, 1000, 1)
	splitOne(t, l, 0, "c", "u")
	// 事件时刻：4、5、9、10；冻结 [5,10) 只覆盖 5 与 9
	for _, ev := range []struct {
		id string
		at int64
	}{{"e4", 4}, {"e5", 5}, {"e9", 9}, {"e10", 10}} {
		if _, err := l.Earn(ev.at, ev.id, "c", 100); err != nil {
			t.Fatalf("Earn %s: %v", ev.id, err)
		}
	}
	if err := l.Hold(11, "h", "c", 5, 10); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	bal := l.Balance("u", 11)
	if bal.Held != 200 || bal.Pending != 200 {
		t.Fatalf("半开区间：held=%d pending=%d, want held=200 pending=200", bal.Held, bal.Pending)
	}
}

func TestHoldCoversLateEvents(t *testing.T) {
	l := mustLedger(t, 1000, 1)
	splitOne(t, l, 0, "c", "u")
	if err := l.Hold(1, "h", "c", 5, 20); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	// 冻结之后才入账、时刻落在区间内的事件同样被冻结
	if _, err := l.Earn(10, "e1", "c", 100); err != nil {
		t.Fatalf("Earn: %v", err)
	}
	// 时刻恰为 to（半开区间外）的不被冻结
	if _, err := l.Earn(20, "e2", "c", 100); err != nil {
		t.Fatalf("Earn: %v", err)
	}
	bal := l.Balance("u", 20)
	if bal.Held != 100 || bal.Pending != 100 {
		t.Fatalf("后到事件：held=%d pending=%d, want held=100 pending=100", bal.Held, bal.Pending)
	}
}

func TestMultiHoldStacking(t *testing.T) {
	l := mustLedger(t, 0, 1)
	splitOne(t, l, 0, "c", "u")
	if _, err := l.Earn(0, "e1", "c", 100); err != nil {
		t.Fatalf("Earn: %v", err)
	}
	if err := l.Hold(1, "h1", "c", 0, 10); err != nil {
		t.Fatalf("Hold h1: %v", err)
	}
	if err := l.Hold(2, "h2", "c", 0, 10); err != nil {
		t.Fatalf("Hold h2: %v", err)
	}
	if bal := l.Balance("u", 2); bal.Held != 100 || bal.Available != 0 {
		t.Fatalf("双冻结：held=%d available=%d", bal.Held, bal.Available)
	}
	// 解除一条后仍被另一条覆盖
	if err := l.Release(3, "h1"); err != nil {
		t.Fatalf("Release h1: %v", err)
	}
	if bal := l.Balance("u", 3); bal.Held != 100 || bal.Available != 0 {
		t.Fatalf("解除一条后：held=%d available=%d", bal.Held, bal.Available)
	}
	// 全部解除后才解冻
	if err := l.Release(4, "h2"); err != nil {
		t.Fatalf("Release h2: %v", err)
	}
	if bal := l.Balance("u", 4); bal.Held != 0 || bal.Available != 100 {
		t.Fatalf("全部解除后：held=%d available=%d", bal.Held, bal.Available)
	}
}

func TestHoldNotRetroactiveOnPaid(t *testing.T) {
	l := mustLedger(t, 0, 1)
	splitOne(t, l, 0, "c", "u")
	if _, err := l.Earn(0, "e1", "c", 100); err != nil {
		t.Fatalf("Earn: %v", err)
	}
	paid, _, err := l.Settle(1, "u")
	if err != nil || paid != 100 {
		t.Fatalf("Settle paid=%d err=%v", paid, err)
	}
	// 已付出的部分不追溯：冻结覆盖已付清事件，held 仍为 0
	if err := l.Hold(2, "h", "c", 0, 10); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if bal := l.Balance("u", 2); bal.Held != 0 {
		t.Fatalf("held=%d, want 0（已付清不追溯）", bal.Held)
	}
}

func TestHoldReleaseErrors(t *testing.T) {
	l := mustLedger(t, 0, 1)
	splitOne(t, l, 10, "c", "u")
	if err := l.Hold(20, "h1", "c", 0, 10); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"Hold参数非法优先于时钟回退", func() error { return l.Hold(5, "x", "c", 10, 10) }, revenue.ErrInvalidParam},
		{"Hold区间from不小于to", func() error { return l.Hold(30, "x", "c", 10, 10) }, revenue.ErrInvalidParam},
		{"Hold区间from为负", func() error { return l.Hold(30, "x", "c", -1, 10) }, revenue.ErrInvalidParam},
		{"Hold空ID", func() error { return l.Hold(30, "", "c", 0, 10) }, revenue.ErrInvalidParam},
		{"Hold时钟回退", func() error { return l.Hold(15, "x", "c", 0, 10) }, revenue.ErrClockRewind},
		{"Hold内容无分成表", func() error { return l.Hold(30, "x", "nosplit", 0, 10) }, revenue.ErrNoSplit},
		{"Hold冻结已存在", func() error { return l.Hold(30, "h1", "c", 0, 10) }, hold.ErrHoldExists},
		{"Release参数非法优先于时钟回退", func() error { return l.Release(5, "") }, revenue.ErrInvalidParam},
		{"Release时钟回退", func() error { return l.Release(15, "h1") }, revenue.ErrClockRewind},
		{"Release冻结不存在", func() error { return l.Release(30, "nosuch") }, hold.ErrHoldNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒不改状态含时钟：h1 仍存在，t=20 的操作仍被接受
	if err := l.Release(20, "h1"); err != nil {
		t.Fatalf("Release h1 after rejections: %v", err)
	}
	if err := l.Release(30, "h1"); !errors.Is(err, hold.ErrHoldNotFound) {
		t.Fatalf("重复解除 err=%v, want ErrHoldNotFound", err)
	}
}
