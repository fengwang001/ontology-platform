package pool

import (
	"errors"
	"fmt"
	"testing"

	"ontology/loan"
	"ontology/recall"
)

func mustNew(t *testing.T, n, pen int64) *Pool {
	t.Helper()
	p, err := New(n, pen)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", n, pen, err)
	}
	return p
}

func TestNewInvalid(t *testing.T) {
	cases := []struct{ n, pen int64 }{
		{0, 500}, {1_000_001, 500}, {10, -1}, {10, 10001},
	}
	for _, c := range cases {
		if _, err := New(c.n, c.pen); !errors.Is(err, ErrInvalid) {
			t.Fatalf("New(%d,%d) err=%v, want ErrInvalid", c.n, c.pen, err)
		}
	}
}

func cqty(p *Pool, id int64) int64 {
	if c, ok := p.Contract(id); ok {
		return c.Qty
	}
	return 0
}

func idleOf(p *Pool, sym, lender string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.book.Idle(loan.ID(sym), loan.ID(lender))
}

func penaltyOf(r, price, pen int64) int64 { return recall.Penalty(r, price, pen) }

var bsSym = []byte("S")

// TestWorkedExample 复现题面 N=10 pen=500 price=21 的完整样例。
func TestWorkedExample(t *testing.T) {
	p := mustNew(t, 10, 500)
	must := func(err error, step string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	L1, L2, B1, B2 := []byte("L1"), []byte("L2"), []byte("B1"), []byte("B2")
	must(p.SetPrice(1, bsSym, 21), "SetPrice")
	must(p.Lend(1, L1, bsSym, 500), "Lend L1")
	must(p.Lend(2, L2, bsSym, 300), "Lend L2")

	ids, err := p.Borrow(3, B1, bsSym, 600)
	must(err, "Borrow B1")
	if fmt.Sprint(ids) != "[1 2]" {
		t.Fatalf("B1 contracts=%v, want [1 2]", ids)
	}
	if cqty(p, 1) != 500 || cqty(p, 2) != 100 {
		t.Fatalf("c1=%d c2=%d, want 500/100", cqty(p, 1), cqty(p, 2))
	}
	ids3, err := p.Borrow(4, B2, bsSym, 100)
	must(err, "Borrow B2")
	if fmt.Sprint(ids3) != "[3]" || cqty(p, 3) != 100 {
		t.Fatalf("B2 contracts=%v", ids3)
	}

	must(p.Withdraw(5, L1, bsSym, 400), "Withdraw L1")
	c1, _ := p.Contract(1)
	c4, ok4 := p.Contract(4)
	c5, ok5 := p.Contract(5)
	if c1.Qty != 100 || !ok4 || c4.Lender != "L2" || c4.Borrower != "B1" || c4.Qty != 100 {
		t.Fatalf("after withdraw c1=%d c4=%+v ok=%v", c1.Qty, c4, ok4)
	}
	if !ok5 || c5.Qty != 300 || c5.Kind != loan.Recalled || c5.Dl != 15 {
		t.Fatalf("recall c5=%+v ok=%v", c5, ok5)
	}

	must(p.Return(8, B1, bsSym, 250), "Return B1 t8")
	if cqty(p, 5) != 50 {
		t.Fatalf("c5=%d, want 50", cqty(p, 5))
	}

	must(p.Lend(15, L2, bsSym, 1), "op at t15 triggers settlement")
	bs := p.Buyins()
	if len(bs) != 1 {
		t.Fatalf("buyins=%v", bs)
	}
	b := bs[0]
	if b.ContractID != 5 || b.Qty != 50 || b.Price != 21 || b.Penalty != 53 || b.At != 15 {
		t.Fatalf("buyin=%+v, want id5 qty50 price21 pen53 at15", b)
	}
	if got := p.Payable(B1); got != 1103 {
		t.Fatalf("payable=%d, want 1103", got)
	}
	if cqty(p, 1) != 100 || cqty(p, 2) != 100 || cqty(p, 4) != 100 {
		t.Fatalf("remaining debts c1=%d c2=%d c4=%d", cqty(p, 1), cqty(p, 2), cqty(p, 4))
	}
}

func scenarioWithRecall(t *testing.T) *Pool {
	t.Helper()
	p := mustNew(t, 10, 500)
	L1, L2, B1 := []byte("L1"), []byte("L2"), []byte("B1")
	B2 := []byte("B2")
	for _, a := range []func() error{
		func() error { return p.SetPrice(1, bsSym, 21) },
		func() error { return p.Lend(1, L1, bsSym, 500) },
		func() error { return p.Lend(2, L2, bsSym, 300) },
		func() error { _, e := p.Borrow(3, B1, bsSym, 600); return e },
		func() error { _, e := p.Borrow(4, B2, bsSym, 100); return e },
		func() error { return p.Withdraw(5, L1, bsSym, 400) },
	} {
		if err := a(); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// TestEarlyReturnThenOrdinary：t14 还 60，先召回 50 再普通 10 回到 L1 空闲。
func TestEarlyReturnThenOrdinary(t *testing.T) {
	p := scenarioWithRecall(t)
	if err := p.Return(8, []byte("B1"), bsSym, 250); err != nil {
		t.Fatal(err) // c5: 300 -> 50
	}
	if err := p.Return(14, []byte("B1"), bsSym, 60); err != nil {
		t.Fatal(err)
	}
	if cqty(p, 5) != 0 {
		t.Fatalf("c5=%d, want 0", cqty(p, 5))
	}
	if cqty(p, 1) != 90 {
		t.Fatalf("c1=%d, want 90", cqty(p, 1))
	}
	if idle := idleOf(p, "S", "L1"); idle != 10 {
		t.Fatalf("L1 idle=%d, want 10", idle)
	}
	if err := p.SetPrice(15, bsSym, 21); err != nil {
		t.Fatal(err)
	}
	if len(p.Buyins()) != 0 {
		t.Fatalf("settled recall must not buy in, got %v", p.Buyins())
	}
}

// TestDueBoundary：dl 恰等于 now 到期，now=dl-1 不到期。
func TestDueBoundary(t *testing.T) {
	p := scenarioWithRecall(t)
	if err := p.SetPrice(14, bsSym, 30); err != nil {
		t.Fatal(err)
	}
	if len(p.Buyins()) != 0 {
		t.Fatalf("at dl-1 no buyin, got %v", p.Buyins())
	}
	if err := p.SetPrice(15, bsSym, 30); err != nil {
		t.Fatal(err)
	}
	bs := p.Buyins()
	if len(bs) != 1 || bs[0].At != 15 || bs[0].Price != 30 {
		t.Fatalf("at dl buyin=%v", bs)
	}
}

// TestSetPriceUsesOldPrice：到期结算在改价之前，用旧价买入。
func TestSetPriceUsesOldPrice(t *testing.T) {
	p := scenarioWithRecall(t)
	if err := p.SetPrice(15, bsSym, 999); err != nil {
		t.Fatal(err)
	}
	bs := p.Buyins()
	if len(bs) != 1 || bs[0].Price != 21 {
		t.Fatalf("buyin price=%v, want old 21", bs)
	}
	if price, _ := p.Price(bsSym); price != 999 {
		t.Fatalf("new price=%d, want 999", price)
	}
}

// TestPenaltyCeil 覆盖向上取整与零罚金。
func TestPenaltyCeil(t *testing.T) {
	cases := []struct{ r, price, pen, want int64 }{
		{50, 21, 500, 53},
		{1, 1, 1, 1},
		{3, 7, 100, 1},
		{50, 21, 0, 0},
		{100, 10_000, 10_000, 1_000_000},
	}
	for _, c := range cases {
		if got := penaltyOf(c.r, c.price, c.pen); got != c.want {
			t.Fatalf("penalty(%d,%d,%d)=%d want %d", c.r, c.price, c.pen, got, c.want)
		}
	}
}
