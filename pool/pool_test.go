package pool_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/pool"
	"ontology/recall"
)

func b(s string) []byte { return []byte(s) }

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name string
		n    int64
		pen  int64
		want error
	}{
		{"ok", 10, 500, nil},
		{"n zero", 0, 500, pool.ErrInvalid},
		{"n too big", 1_000_001, 0, pool.ErrInvalid},
		{"pen neg", 10, -1, pool.ErrInvalid},
		{"pen too big", 10, 10001, pool.ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pool.NewPool(c.n, c.pen)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want=%v", err, c.want)
			}
		})
	}
}

func TestArgumentValidation(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	type call struct {
		name string
		fn   func() error
	}
	calls := []call{
		{"bad now", func() error { _, e := p.Lend(-1, b("L"), b("S"), 1); return e }},
		{"now too big", func() error { _, e := p.Lend(1_000_000_000_001, b("L"), b("S"), 1); return e }},
		{"empty lender", func() error { _, e := p.Lend(1, b(""), b("S"), 1); return e }},
		{"empty sym", func() error { _, e := p.Lend(1, b("L"), b(""), 1); return e }},
		{"qty zero", func() error { _, e := p.Lend(1, b("L"), b("S"), 0); return e }},
		{"qty big", func() error { _, e := p.Lend(1, b("L"), b("S"), 1_000_000_001); return e }},
		{"price zero", func() error { _, e := p.SetPrice(1, b("S2"), 0); return e }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(); !errors.Is(err, pool.ErrInvalid) {
				t.Fatalf("err=%v want ErrInvalid", err)
			}
		})
	}
}

func TestClockRegressionNoStateChange(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(5, b("S"), 10)
	if _, err := p.Lend(3, b("L"), b("S"), 5); !errors.Is(err, pool.ErrClock) {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if got := p.Now(); got != 5 {
		t.Fatalf("clock changed: %d", got)
	}
	if got := p.Free(b("L"), b("S")); got != 0 {
		t.Fatalf("state changed, free=%d", got)
	}
}

func TestRejectOrder(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(10, b("S"), 10)
	if _, err := p.Lend(5, b(""), b("S"), 0); !errors.Is(err, pool.ErrInvalid) {
		t.Fatalf("invalid-first: %v", err)
	}
	if _, err := p.Lend(9, b("L"), b("NOPE"), 1); !errors.Is(err, pool.ErrClock) {
		t.Fatalf("clock-before-notfound: %v", err)
	}
	if _, err := p.Lend(11, b("L"), b("NOPE"), 1); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("notfound: %v", err)
	}
	if _, err := p.Borrow(11, b("B"), b("NOPE"), 1); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("borrow-notfound: %v", err)
	}
	if _, err := p.Withdraw(11, b("X"), b("S"), 1); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("withdraw-notfound: %v", err)
	}
	if _, err := p.Return(11, b("X"), b("S"), 1); !errors.Is(err, pool.ErrNotFound) {
		t.Fatalf("return-notfound: %v", err)
	}
}

func TestBorrowAllOrNothingAndOrder(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	r1, _ := p.Lend(1, b("L1"), b("S"), 500)
	r2, _ := p.Lend(2, b("L2"), b("S"), 300)
	if !r1.Registered || r1.RegSeq != 1 || !r2.Registered || r2.RegSeq != 2 {
		t.Fatalf("reg seq: %+v %+v", r1, r2)
	}
	if _, err := p.Borrow(3, b("B1"), b("S"), 801); !errors.Is(err, pool.ErrNoSupply) {
		t.Fatalf("want no supply, got %v", err)
	}
	if got := p.Free(b("L1"), b("S")); got != 500 {
		t.Fatalf("free changed after fail: %d", got)
	}
	br, err := p.Borrow(3, b("B1"), b("S"), 600)
	if err != nil || len(br.Contracts) != 2 {
		t.Fatalf("borrow: %+v %v", br, err)
	}
	if c := br.Contracts[0]; c.ID != 1 || string(c.Lender) != "L1" || c.Qty != 500 {
		t.Fatalf("c1=%+v", c)
	}
	if c := br.Contracts[1]; c.ID != 2 || string(c.Lender) != "L2" || c.Qty != 100 {
		t.Fatalf("c2=%+v", c)
	}
	if got := p.Free(b("L2"), b("S")); got != 200 {
		t.Fatalf("L2 free=%d", got)
	}
}

func setupExample(t *testing.T) *pool.Pool {
	t.Helper()
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 21)
	p.Lend(1, b("L1"), b("S"), 500)
	p.Lend(2, b("L2"), b("S"), 300)
	p.Borrow(3, b("B1"), b("S"), 600)
	p.Borrow(4, b("B2"), b("S"), 100)
	wr, err := p.Withdraw(5, b("L1"), b("S"), 400)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if len(wr.Replacements) != 1 || wr.Replacements[0].ID != 4 || wr.Replacements[0].Qty != 100 {
		t.Fatalf("replacement=%+v", wr.Replacements)
	}
	if len(wr.Recalls) != 1 || wr.Recalls[0].ID != 5 || wr.Recalls[0].Qty != 300 || wr.Recalls[0].DL != 15 {
		t.Fatalf("recall=%+v", wr.Recalls)
	}
	return p
}

func TestExampleReturnThenForceBuy(t *testing.T) {
	p := setupExample(t)
	r, err := p.Return(8, b("B1"), b("S"), 250)
	if err != nil || r.ToRecalled != 250 || r.ToNormal != 0 {
		t.Fatalf("return8: %+v %v", r, err)
	}
	rcs := p.RecallContracts()
	if len(rcs) != 1 || rcs[0].ID != 5 || rcs[0].Qty != 50 {
		t.Fatalf("recall after return=%+v", rcs)
	}
	lr, err := p.Lend(15, b("L3"), b("S"), 1)
	if err != nil {
		t.Fatalf("lend15: %v", err)
	}
	if len(lr.Buys) != 1 {
		t.Fatalf("buys=%+v", lr.Buys)
	}
	want := recall.Buy{ID: 5, Qty: 50, Price: 21, Pen: 53, Amount: 1103, Time: 15}
	if lr.Buys[0] != want {
		t.Fatalf("buy=%+v want=%+v", lr.Buys[0], want)
	}
	if got := p.Debt(b("B1"), b("S")); got != 300 {
		t.Fatalf("B1 debt=%d want 300", got)
	}
	if got := p.Debt(b("B2"), b("S")); got != 100 {
		t.Fatalf("B2 debt=%d", got)
	}
	ids := map[int64]int64{}
	for _, c := range p.NormalContracts(b("S")) {
		if string(c.Borrower) == "B1" {
			ids[c.ID] = c.Qty
		}
	}
	if ids[1] != 100 || ids[4] != 100 {
		t.Fatalf("B1 contracts=%v", ids)
	}
}

func TestExampleReturn14Branches(t *testing.T) {
	p := setupExample(t)
	r, err := p.Return(14, b("B1"), b("S"), 60)
	if err != nil || r.ToRecalled != 60 || r.ToNormal != 0 {
		t.Fatalf("return14: %+v %v", r, err)
	}
	rcs := p.RecallContracts()
	if len(rcs) != 1 || rcs[0].ID != 5 || rcs[0].Qty != 240 {
		t.Fatalf("recall=%+v", rcs)
	}
	if got := p.Free(b("L1"), b("S")); got != 0 {
		t.Fatalf("L1 free=%d", got)
	}
	var c1 int64 = -1
	for _, c := range p.NormalContracts(b("S")) {
		if c.ID == 1 {
			c1 = c.Qty
		}
	}
	if c1 != 100 {
		t.Fatalf("contract1 qty=%d", c1)
	}
	// 再还 240+ 触发先召回后普通：240 了结合约5，10 回到合约1。
	r2, err := p.Return(14, b("B1"), b("S"), 250)
	if err != nil || r2.ToRecalled != 240 || r2.ToNormal != 10 {
		t.Fatalf("return14b: %+v %v", r2, err)
	}
	if got := p.Free(b("L1"), b("S")); got != 10 {
		t.Fatalf("L1 free after normal return=%d", got)
	}
	if _, err := p.Lend(15, b("L2"), b("S"), 1); err != nil {
		t.Fatalf("lend15: %v", err)
	}
	if len(p.Buys()) != 0 {
		t.Fatalf("unexpected buys: %+v", p.Buys())
	}
}

func TestDLBoundary(t *testing.T) {
	p := setupExample(t)
	if _, err := p.Lend(14, b("L2"), b("S"), 1); err != nil || len(p.Buys()) != 0 {
		t.Fatalf("t14 buys=%v err=%v", p.Buys(), err)
	}
	if _, err := p.Lend(15, b("L2"), b("S"), 1); err != nil || len(p.Buys()) != 1 {
		t.Fatalf("t15 buys=%v err=%v", p.Buys(), err)
	}
}

func TestSetPriceUsesOldPrice(t *testing.T) {
	p := setupExample(t)
	p.Return(8, b("B1"), b("S"), 250)
	buys, err := p.SetPrice(15, b("S"), 999)
	if err != nil {
		t.Fatalf("setprice: %v", err)
	}
	if len(buys) != 1 || buys[0].Price != 21 || buys[0].Amount != 1103 {
		t.Fatalf("old-price buy=%+v", buys)
	}
	if got, ok := p.Price(b("S")); !ok || got != 999 {
		t.Fatalf("new price=%d ok=%v", got, ok)
	}
}

func TestPenaltyCeil(t *testing.T) {
	// pen=1（万一）：r*p=50*21=1050，罚金 ceil(0.105)=1。
	p, _ := pool.NewPool(10, 1)
	p.SetPrice(0, b("S"), 21)
	p.Lend(1, b("L1"), b("S"), 50)
	p.Borrow(2, b("B1"), b("S"), 50)
	p.Withdraw(3, b("L1"), b("S"), 50) // dl=13
	lr, err := p.Lend(13, b("L2"), b("S"), 1)
	if err != nil {
		t.Fatalf("lend: %v", err)
	}
	buys := lr.Buys
	if len(buys) != 1 || buys[0].Pen != 1 || buys[0].Amount != 1051 {
		t.Fatalf("buy=%+v", buys)
	}
}

func TestRejectionAfterSettlementKeepsBuy(t *testing.T) {
	p := setupExample(t)
	p.Return(8, b("B1"), b("S"), 250) // 合约5剩50, dl=15
	// 合法时钟、存在借入人，但超量：先结算（买入50），再报超量，不回滚。
	_, err := p.Return(15, b("B1"), b("S"), 1_000_000_000)
	if !errors.Is(err, pool.ErrOverQty) {
		t.Fatalf("want overqty, got %v", err)
	}
	buys := p.Buys()
	if len(buys) != 1 || buys[0].ID != 5 || buys[0].Amount != 1103 {
		t.Fatalf("settlement not kept: %+v", buys)
	}
	// 时钟已推进到15。
	if p.Now() != 15 {
		t.Fatalf("clock=%d", p.Now())
	}
}

func TestWithdrawFreeFirstAndOverQty(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 100)
	p.Borrow(2, b("B1"), b("S"), 60) // L1 空闲40，合约1量60
	// 撤回 90 <= 空闲40+合约60。
	wr, err := p.Withdraw(3, b("L1"), b("S"), 90)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if len(wr.Recalls) != 1 || wr.Recalls[0].Qty != 50 {
		t.Fatalf("recall=%+v", wr.Recalls)
	}
	if got := p.Free(b("L1"), b("S")); got != 0 {
		t.Fatalf("free=%d", got)
	}
	// 再撤回超量：剩余普通合约仅10。
	if _, err := p.Withdraw(4, b("L1"), b("S"), 11); !errors.Is(err, pool.ErrOverQty) {
		t.Fatalf("want overqty, got %v", err)
	}
	// 累计Lend100 = 空闲0 + 普通10 + 召回50 + 交还40。
	tl, fs, ls, rt := p.Invariant(b("S"))
	if tl != 100 || fs != 0 || ls != 60 || rt != 40 || tl != fs+ls+rt {
		t.Fatalf("invariant %d %d %d %d", tl, fs, ls, rt)
	}
}

func TestReplacementSkipsOwnFree(t *testing.T) {
	// L1 自己有空闲时撤回：先取自有空闲，绝不把自有空闲当作“他人接手”。
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 100)
	p.Lend(2, b("L2"), b("S"), 0+50)
	p.Borrow(3, b("B1"), b("S"), 80) // L1: free20 合约80；L2: free50
	wr, err := p.Withdraw(4, b("L1"), b("S"), 70)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	// 先自有空闲20；合约80 中再由 L2 接手50；无召回（20+50=70）。
	if len(wr.Replacements) != 1 {
		t.Fatalf("replacements=%+v", wr.Replacements)
	}
	rc := wr.Replacements[0]
	if string(rc.Lender) != "L2" || rc.Qty != 50 {
		t.Fatalf("replacement=%+v", rc)
	}
	if len(wr.Recalls) != 0 {
		t.Fatalf("unexpected recall=%+v", wr.Recalls)
	}
	if got := p.Free(b("L1"), b("S")); got != 0 {
		t.Fatalf("L1 free=%d", got)
	}
	if got := p.Free(b("L2"), b("S")); got != 0 {
		t.Fatalf("L2 free=%d", got)
	}
}

func TestDescendingContractOrderAndNewLendNoTakeover(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 100)
	p.Lend(2, b("L2"), b("S"), 100)
	p.Borrow(2, b("B1"), b("S"), 20) // 合约1: L1 20, L1 free80, L2 free100
	p.Borrow(2, b("B1"), b("S"), 30) // 合约2: L1 30 (L1 free50), L2 free100
	// 撤回 60：自有空闲50先交还；剩10 按降序先处理合约2（30 中取10），合约1不动。
	wr, err := p.Withdraw(3, b("L1"), b("S"), 60)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	// 合约2 的10 由 L2 接手（新合约3），合约2剩20；合约1完全不动 => 证明降序。
	if len(wr.Replacements) != 1 || wr.Replacements[0].ID != 3 {
		t.Fatalf("replacements=%+v", wr.Replacements)
	}
	if r := wr.Replacements[0]; string(r.Lender) != "L2" || r.Qty != 10 {
		t.Fatalf("replacement=%+v", r)
	}
	if len(wr.Recalls) != 0 {
		t.Fatalf("recalls=%+v", wr.Recalls)
	}
	var c1 int64
	var c2 int64
	for _, c := range p.NormalContracts(b("S")) {
		if c.ID == 1 {
			c1 = c.Qty
		}
		if c.ID == 2 {
			c2 = c.Qty
		}
	}
	if c1 != 20 || c2 != 20 {
		t.Fatalf("contract1=%d contract2=%d (descending order violated)", c1, c2)
	}
}

func TestNewLendDoesNotTakeOverRecall(t *testing.T) {
	// 撤回时无人接手 => 召回；之后 L2 新 Lend，不会自动接手召回。
	p, _ := pool.NewPool(100, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 10)
	p.Borrow(2, b("B1"), b("S"), 10)
	wr, _ := p.Withdraw(3, b("L1"), b("S"), 10)
	if len(wr.Recalls) != 1 || len(wr.Replacements) != 0 {
		t.Fatalf("wr=%+v", wr)
	}
	p.Lend(4, b("L2"), b("S"), 100)
	rcs := p.RecallContracts()
	if len(rcs) != 1 || rcs[0].Qty != 10 || string(rcs[0].Lender) != "L1" {
		t.Fatalf("recall changed after new lend: %+v", rcs)
	}
	// 新空闲确实可被新的 Borrow 使用。
	br, err := p.Borrow(5, b("B2"), b("S"), 100)
	if err != nil || len(br.Contracts) != 1 || string(br.Contracts[0].Lender) != "L2" {
		t.Fatalf("borrow=%+v err=%v", br, err)
	}
}

func TestReturnRecallBeforeNormal(t *testing.T) {
	// 构造 B1 同时欠召回与普通：先 L1 借出形成召回，再让 L2 借出形成普通。
	p, _ := pool.NewPool(100, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 10)
	p.Borrow(2, b("B1"), b("S"), 10)
	p.Withdraw(3, b("L1"), b("S"), 10) // 召回合约，dl=103
	p.Lend(4, b("L2"), b("S"), 10)
	p.Borrow(5, b("B1"), b("S"), 10) // 普通合约2
	r, err := p.Return(6, b("B1"), b("S"), 12)
	if err != nil || r.ToRecalled != 10 || r.ToNormal != 2 {
		t.Fatalf("return=%+v %v", r, err)
	}
	if len(p.RecallContracts()) != 0 {
		t.Fatalf("recall remains: %+v", p.RecallContracts())
	}
	if got := p.Free(b("L2"), b("S")); got != 2 {
		t.Fatalf("L2 free=%d", got)
	}
}

func TestReturnOverQty(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	p.Lend(1, b("L1"), b("S"), 10)
	p.Borrow(2, b("B1"), b("S"), 10)
	if _, err := p.Return(3, b("B1"), b("S"), 11); !errors.Is(err, pool.ErrOverQty) {
		t.Fatalf("want overqty, got %v", err)
	}
	if got := p.Debt(b("B1"), b("S")); got != 10 {
		t.Fatalf("debt changed: %d", got)
	}
}

func TestScannedIndependentOfZeroFreeLenders(t *testing.T) {
	for _, n := range []int{10, 10000} {
		name := fmtName(n)
		t.Run(name, func(t *testing.T) {
			p, _ := pool.NewPool(10, 500)
			p.SetPrice(0, b("S"), 10)
			for i := 0; i < n; i++ {
				p.Lend(int64(i+1), b("L"+itoa(i)), b("S"), 1)
				p.Borrow(int64(i+1), b("D"), b("S"), 1) // 全部借空 => 空闲0
			}
			// 再新增 2 个真有空闲的出借人。
			p.Lend(int64(n+1), b("LA"), b("S"), 5)
			p.Lend(int64(n+2), b("LB"), b("S"), 5)
			br, err := p.Borrow(int64(n+3), b("B"), b("S"), 7)
			if err != nil || len(br.Contracts) != 2 {
				t.Fatalf("borrow: %+v %v", br, err)
			}
			if got := p.Scanned(); got != 2 {
				t.Fatalf("n=%d scanned=%d want 2 (<= 出券2+1=3)", n, got)
			}
		})
	}
}

func fmtName(n int) string {
	if n == 10 {
		return "ten-zero-free-lenders"
	}
	return "tenthousand-zero-free-lenders"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestConcurrencyEquivalentToSerial(t *testing.T) {
	p, _ := pool.NewPool(10, 500)
	p.SetPrice(0, b("S"), 10)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.Lend(1, b("L"+itoa(i)), b("S"), 10)
		}(i)
	}
	wg.Wait()
	if _, f, _, _ := p.Invariant(b("S")); f != 200 {
		t.Fatalf("free sum=%d want 200", f)
	}
	br, err := p.Borrow(2, b("B"), b("S"), 200)
	if err != nil {
		t.Fatalf("borrow: %v", err)
	}
	if len(br.Contracts) != 20 {
		t.Fatalf("contracts=%d", len(br.Contracts))
	}
}
