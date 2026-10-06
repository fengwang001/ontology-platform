package instr

import (
	"errors"
	"testing"
)

func mustBook(t *testing.T, U, rs, rb int64, A int) *Book {
	t.Helper()
	b, err := New(U, rs, rb, A)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", U, rs, rb, A, err)
	}
	return b
}

func TestNewParams(t *testing.T) {
	cases := []struct {
		name      string
		U, rs, rb int64
		A         int
		wantErr   bool
	}{
		{"ok-min", 1, 0, 0, 1, false},
		{"ok-max", 1_000_000, 10_000, 10_000, 100, false},
		{"U-zero", 0, 0, 0, 1, true},
		{"U-too-big", 1_000_001, 0, 0, 1, true},
		{"rs-neg", 1, -1, 0, 1, true},
		{"rs-too-big", 1, 10_001, 0, 1, true},
		{"rb-neg", 1, 0, -1, 1, true},
		{"rb-too-big", 1, 0, 10_001, 1, true},
		{"A-zero", 1, 0, 0, 0, true},
		{"A-too-big", 1, 0, 0, 101, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.U, c.rs, c.rb, c.A)
			if c.wantErr && !errors.Is(err, ErrParam) {
				t.Fatalf("want ErrParam, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreditCashPriceParams(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"credit-ok", func() error { return b.Credit(0, "S", "X", 450) }, nil},
		{"credit-empty-acct", func() error { return b.Credit(0, "", "X", 1) }, ErrParam},
		{"credit-empty-sym", func() error { return b.Credit(0, "S", "", 1) }, ErrParam},
		{"credit-zero", func() error { return b.Credit(0, "S", "X", 0) }, ErrParam},
		{"credit-too-big", func() error { return b.Credit(0, "S", "X", 1_000_000_000_001) }, ErrParam},
		{"cash-ok", func() error { return b.CreditCash(0, "B", 20_000) }, nil},
		{"cash-empty-acct", func() error { return b.CreditCash(0, "", 1) }, ErrParam},
		{"cash-zero", func() error { return b.CreditCash(0, "B", 0) }, ErrParam},
		{"cash-too-big", func() error { return b.CreditCash(0, "B", 1_000_000_000_001) }, ErrParam},
		{"price-ok", func() error { return b.SetPrice(0, "X", 11) }, nil},
		{"price-empty-sym", func() error { return b.SetPrice(0, "", 1) }, ErrParam},
		{"price-zero", func() error { return b.SetPrice(0, "X", 0) }, ErrParam},
		{"price-too-big", func() error { return b.SetPrice(0, "X", 1_000_001) }, ErrParam},
		{"day-negative", func() error { return b.Credit(-1, "S", "X", 1) }, ErrParam},
		{"day-too-big", func() error { return b.Credit(1_000_001, "S", "X", 1) }, ErrParam},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.op(); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	st := b.Snapshot()
	if got := st.Accounts["S"].Holdings["X"]; got != 450 {
		t.Fatalf("S holdings = %d, want 450", got)
	}
	if got := st.Accounts["B"].Cash; got != 20_000 {
		t.Fatalf("B cash = %d, want 20000", got)
	}
}

func TestDateRollback(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	if err := b.Credit(5, "S", "X", 100); err != nil {
		t.Fatal(err)
	}
	if err := b.Credit(4, "S", "X", 100); !errors.Is(err, ErrDate) {
		t.Fatalf("want ErrDate, got %v", err)
	}
	if err := b.CreditCash(3, "S", 100); !errors.Is(err, ErrDate) {
		t.Fatalf("want ErrDate, got %v", err)
	}
	if err := b.SetPrice(0, "X", 1); !errors.Is(err, ErrDate) {
		t.Fatalf("want ErrDate, got %v", err)
	}
	// 同日不属回退。
	if err := b.Credit(5, "S", "X", 100); err != nil {
		t.Fatalf("same day should be accepted: %v", err)
	}
	st := b.Snapshot()
	if got := st.Accounts["S"].Holdings["X"]; got != 200 {
		t.Fatalf("rejected ops must not change state, holdings = %d, want 200", got)
	}
}

func TestInstructValidation(t *testing.T) {
	setup := func(t *testing.T) *Book {
		b := mustBook(t, 100, 10, 5, 2)
		if err := b.SetPrice(0, "X", 11); err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		name                   string
		day                    int
		id, seller, buyer, sym string
		qty, amount            int64
		sd                     int
		want                   error
	}{
		{"ok", 0, "i1", "S", "B", "X", 1000, 10005, 2, nil},
		{"empty-id", 0, "", "S", "B", "X", 100, 1, 2, ErrParam},
		{"empty-seller", 0, "i2", "", "B", "X", 100, 1, 2, ErrParam},
		{"empty-buyer", 0, "i2", "S", "", "X", 100, 1, 2, ErrParam},
		{"empty-sym", 0, "i2", "S", "B", "", 100, 1, 2, ErrParam},
		{"qty-zero", 0, "i2", "S", "B", "X", 0, 1, 2, ErrParam},
		{"qty-not-multiple", 0, "i2", "S", "B", "X", 150, 1, 2, ErrParam},
		{"qty-too-big", 0, "i2", "S", "B", "X", 1_000_000_100, 1, 2, ErrParam},
		{"amount-zero", 0, "i2", "S", "B", "X", 100, 0, 2, ErrParam},
		{"amount-too-big", 0, "i2", "S", "B", "X", 100, 1_000_000_000_000_001, 2, ErrParam},
		{"sd-not-after-day", 0, "i2", "S", "B", "X", 100, 1, 0, ErrParam},
		{"same-party", 0, "i2", "S", "S", "X", 100, 1, 2, ErrParam},
		{"day-negative", -1, "i2", "S", "B", "X", 100, 1, 2, ErrParam},
		{"no-price", 0, "i2", "S", "B", "Y", 100, 1, 2, ErrNoPrice},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := setup(t)
			err := b.Instruct(c.day, c.id, c.seller, c.buyer, c.sym, c.qty, c.amount, c.sd)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestInstructSeqAndDuplicate(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	if err := b.SetPrice(0, "X", 11); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i1", "i2", "i3"} {
		if err := b.Instruct(0, id, "S", "B", "X", 100, 100, 2); err != nil {
			t.Fatal(err)
		}
	}
	st := b.Snapshot()
	for i, ins := range st.Instructions {
		if ins.Seq != i {
			t.Fatalf("instruction %s seq = %d, want %d", ins.ID, ins.Seq, i)
		}
	}
	if err := b.Instruct(0, "i2", "S", "B", "X", 100, 100, 2); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

// TestRejectPrecedence 校验拒绝按次序只报第一个：
// 参数非法 > 日期回退 > 编号重复/不存在/无现价 > 状态不符。
func TestRejectPrecedence(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	if err := b.SetPrice(0, "X", 11); err != nil {
		t.Fatal(err)
	}
	if err := b.Instruct(1, "i1", "S", "B", "X", 100, 100, 3); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		// 参数非法 优先于 日期回退（day 回退且 qty 非倍数）。
		{"param-over-date", func() error {
			return b.Instruct(0, "i9", "S", "B", "X", 150, 100, 5)
		}, ErrParam},
		// 日期回退 优先于 编号重复。
		{"date-over-duplicate", func() error {
			return b.Instruct(0, "i1", "S", "B", "X", 100, 100, 5)
		}, ErrDate},
		// 编号重复 优先于 无现价。
		{"duplicate-over-noprice", func() error {
			return b.Instruct(1, "i1", "S", "B", "Y", 100, 100, 5)
		}, ErrDuplicate},
		// 编号不存在 优先于 状态不符（对不存在 id 在 sd 当日 Cancel）。
		{"notfound-over-state", func() error {
			return b.Cancel(3, "ghost")
		}, ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.op(); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
}

func TestCancelBoundary(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	if err := b.SetPrice(0, "X", 11); err != nil {
		t.Fatal(err)
	}
	if err := b.Instruct(0, "i1", "S", "B", "X", 100, 100, 3); err != nil {
		t.Fatal(err)
	}
	// day == sd-1：允许。
	if err := b.Cancel(2, "i1"); err != nil {
		t.Fatalf("cancel at sd-1 should succeed: %v", err)
	}
	// 已了结（已取消）再取消：状态不符。
	if err := b.Cancel(2, "i1"); !errors.Is(err, ErrState) {
		t.Fatalf("want ErrState, got %v", err)
	}
	// day == sd：不允许。
	if err := b.Instruct(2, "i2", "S", "B", "X", 100, 100, 3); err != nil {
		t.Fatal(err)
	}
	if err := b.Cancel(3, "i2"); !errors.Is(err, ErrState) {
		t.Fatalf("cancel at sd: want ErrState, got %v", err)
	}
	// 空 id 与不存在 id。
	if err := b.Cancel(3, ""); !errors.Is(err, ErrParam) {
		t.Fatalf("want ErrParam, got %v", err)
	}
	if err := b.Cancel(3, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	st := b.Snapshot()
	if st.Instructions[0].Status != Cancelled {
		t.Fatalf("i1 status = %v, want cancelled", st.Instructions[0].Status)
	}
	if st.Instructions[1].Status != Open {
		t.Fatalf("i2 status = %v, want open", st.Instructions[1].Status)
	}
}

// TestRejectedOpsKeepDate 被拒绝的操作不改任何状态，含日期。
func TestRejectedOpsKeepDate(t *testing.T) {
	b := mustBook(t, 100, 10, 5, 2)
	if err := b.SetPrice(0, "X", 11); err != nil {
		t.Fatal(err)
	}
	if err := b.Credit(5, "S", "X", 100); err != nil {
		t.Fatal(err)
	}
	// 一系列被拒操作（day=10 均不得生效）。
	_ = b.Credit(10, "", "X", 1)                          // 参数非法
	_ = b.Instruct(10, "i1", "S", "S", "X", 100, 100, 20) // 参数非法
	_ = b.Cancel(10, "ghost")                             // 编号不存在
	// 若被拒操作推进了 maxDay，则 day=6 会被判回退。
	if err := b.Credit(6, "S", "X", 100); err != nil {
		t.Fatalf("rejected ops must not advance maxDay: %v", err)
	}
	st := b.Snapshot()
	if got := st.Accounts["S"].Holdings["X"]; got != 200 {
		t.Fatalf("holdings = %d, want 200", got)
	}
	if len(st.Instructions) != 0 {
		t.Fatalf("rejected Instruct must not register, got %d instructions", len(st.Instructions))
	}
}
