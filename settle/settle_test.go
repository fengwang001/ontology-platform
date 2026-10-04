package settle

import (
	"errors"
	"testing"

	"ontology/instr"
)

// checkStep 打印输入/输出/判定依据并核对关键字段。
func checkStep(t *testing.T, res *Result, idx int, want Step) {
	t.Helper()
	got := res.Steps[idx]
	t.Logf("day=%d step=%d id=%s d=%d paid=%d r=%d a=%d b=%d k=%d pay=%d r'=%d "+
		"sellerLiable=%v buyerLiable=%v sFine=%d bFine=%d buyIn=%v cancel=%v comp=%d status=%s",
		res.Day, idx, got.ID, got.BeforeDelivered, got.BeforePaid, got.Remain,
		got.SellerAvail, got.BuyerAfford, got.Deliver, got.Pay, got.AfterRemain,
		got.SellerLiable, got.BuyerLiable, got.SellerFine, got.BuyerFine,
		got.BuyIn, got.Cancelled, got.Comp, got.FinalStatus)
	if got != want {
		t.Fatalf("step %d =\n%+v\nwant\n%+v", idx, got, want)
	}
}

// TestWorkedExample 精确复现题目 i1/i2 三天续例。
func TestWorkedExample(t *testing.T) {
	b := instr.New(100, 10, 5, 2)
	e := New(b)
	for _, op := range []func() error{
		func() error { return b.SetPrice(1, "AAA", 11) },
		func() error { return b.Credit(1, "S", "AAA", 450) },
		func() error { return b.CreditCash(1, "B", 20000) },
		func() error { return b.CreditCash(1, "C", 3000) },
		func() error { return b.Instruct(1, "i1", "S", "B", "AAA", 1000, 10005, 2) },
		func() error { return b.Instruct(1, "i2", "B", "C", "AAA", 600, 6100, 2) },
	} {
		if err := op(); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	res, err := e.RunSettle(2)
	if err != nil {
		t.Fatal(err)
	}
	checkStep(t, res, 0, Step{ID: "i1", SD: 2, Remain: 1000, SellerAvail: 400,
		BuyerAfford: 1000, Deliver: 400, Pay: 4002, AfterRemain: 600,
		SellerLiable: true, SellerFine: 7, FinalStatus: instr.Open})
	checkStep(t, res, 1, Step{ID: "i2", SD: 2, Remain: 600, SellerAvail: 400,
		BuyerAfford: 200, Deliver: 200, Pay: 2033, AfterRemain: 400,
		SellerLiable: true, BuyerLiable: true, SellerFine: 5, BuyerFine: 3,
		FinalStatus: instr.Open})
	if b.Holdings("S", "AAA") != 50 || b.Holdings("B", "AAA") != 200 ||
		b.Holdings("C", "AAA") != 200 {
		t.Fatalf("day2 holdings: S=%d B=%d C=%d",
			b.Holdings("S", "AAA"), b.Holdings("B", "AAA"), b.Holdings("C", "AAA"))
	}
	if b.Cash("S") != 4002 || b.Cash("B") != 20000-4002+2033 || b.Cash("C") != 967 {
		t.Fatalf("day2 cash: S=%d B=%d C=%d", b.Cash("S"), b.Cash("B"), b.Cash("C"))
	}

	res, err = e.RunSettle(3)
	if err != nil {
		t.Fatal(err)
	}
	checkStep(t, res, 0, Step{ID: "i1", SD: 2, BeforeDelivered: 400, BeforePaid: 4002,
		Remain: 600, SellerAvail: 0, BuyerAfford: 600, AfterRemain: 600,
		SellerLiable: true, SellerFine: 7, FinalStatus: instr.Open})
	checkStep(t, res, 1, Step{ID: "i2", SD: 2, BeforeDelivered: 200, BeforePaid: 2033,
		Remain: 400, SellerAvail: 200, BuyerAfford: 0, AfterRemain: 400,
		SellerLiable: true, BuyerLiable: true, SellerFine: 5, BuyerFine: 3,
		FinalStatus: instr.Open})

	res, err = e.RunSettle(4)
	if err != nil {
		t.Fatal(err)
	}
	checkStep(t, res, 0, Step{ID: "i1", SD: 2, BeforeDelivered: 400, BeforePaid: 4002,
		Remain: 600, SellerAvail: 0, BuyerAfford: 600, AfterRemain: 600,
		SellerLiable: true, SellerFine: 7, BuyIn: true, Comp: 597,
		FinalStatus: instr.BoughtIn})
	checkStep(t, res, 1, Step{ID: "i2", SD: 2, BeforeDelivered: 200, BeforePaid: 2033,
		Remain: 400, SellerAvail: 200, BuyerAfford: 0, AfterRemain: 400,
		SellerLiable: true, BuyerLiable: true, SellerFine: 5, BuyerFine: 3,
		BuyIn: true, Comp: 333, FinalStatus: instr.BoughtIn})
	if b.Payable("S") != 7+7+7+597 {
		t.Fatalf("S payable=%d want %d", b.Payable("S"), 7+7+7+597)
	}
	if b.Payable("B") != 5*3+333 { // i2 卖方罚金 5×3 + 买入赔付 333
		t.Fatalf("B payable=%d want %d", b.Payable("B"), 5*3+333)
	}
	if b.Payable("C") != 3+3+3 || b.Receivable("C") != 333 {
		t.Fatalf("C payable=%d receivable=%d", b.Payable("C"), b.Receivable("C"))
	}
	if b.Receivable("B") != 597 {
		t.Fatalf("B receivable=%d want 597", b.Receivable("B"))
	}

	if _, err := e.RunSettle(4); !errors.Is(err, instr.ErrState) {
		t.Fatalf("repeat RunSettle = %v want ErrState", err)
	}
	if _, err := e.RunSettle(2); !errors.Is(err, instr.ErrRollback) {
		t.Fatalf("rollback RunSettle = %v want ErrRollback", err)
	}
}

// TestBuyerAffordOneCent 差 1 分付不起边界。
func TestBuyerAffordOneCent(t *testing.T) {
	if got := buyerAfford(3000, 300, 0, 0, 999, 100, 300); got != 0 {
		t.Fatalf("cash 999: b=%d want 0", got)
	}
	if got := buyerAfford(3000, 300, 0, 0, 1000, 100, 300); got != 100 {
		t.Fatalf("cash 1000: b=%d want 100", got)
	}
	if got := buyerAfford(3000, 300, 100, 1000, 999, 100, 200); got != 0 {
		t.Fatalf("incremental cash 999: b=%d want 0", got)
	}
}

// TestLastDeliveryPaysAll 最后一次交付恰好付清 amount。
func TestLastDeliveryPaysAll(t *testing.T) {
	b := instr.New(100, 0, 0, 100)
	e := New(b)
	if err := b.SetPrice(0, "X", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Credit(0, "S", "X", 300); err != nil {
		t.Fatal(err)
	}
	if err := b.CreditCash(0, "B", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := b.Instruct(0, "i", "S", "B", "X", 300, 10005, 1); err != nil {
		t.Fatal(err)
	}
	res, err := e.RunSettle(1)
	if err != nil {
		t.Fatal(err)
	}
	checkStep(t, res, 0, Step{ID: "i", SD: 1, Remain: 300, SellerAvail: 300,
		BuyerAfford: 300, Deliver: 300, Pay: 10005, AfterRemain: 0,
		FinalStatus: instr.Settled})
	ins, _ := b.Get("i")
	if ins.Delivered != 300 || ins.Paid != 10005 || ins.Status != instr.Settled {
		t.Fatalf("not fully settled: %+v", ins)
	}
}

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
}

// TestChainNoRetry 链式使用当批到货；已处理指令当日不回头。
func TestChainNoRetry(t *testing.T) {
	b := instr.New(100, 0, 0, 100)
	e := New(b)
	must(t, b.SetPrice(0, "X", 10), "price")
	must(t, b.Credit(0, "S1", "X", 100), "credit S1")
	must(t, b.CreditCash(0, "C", 1000), "cash C")
	must(t, b.Instruct(0, "i1", "S1", "B", "X", 200, 2000, 1), "i1")
	must(t, b.Instruct(0, "i2", "B", "C", "X", 200, 1000, 1), "i2")

	res, err := e.RunSettle(1)
	if err != nil {
		t.Fatal(err)
	}
	// i1：卖方只有 100（卖方有责），B 现金 0 → b=0（买方也有责），k=0。
	checkStep(t, res, 0, Step{ID: "i1", SD: 1, Remain: 200, SellerAvail: 100,
		BuyerAfford: 0, AfterRemain: 200, SellerLiable: true, BuyerLiable: true,
		FinalStatus: instr.Open})
	// i2：B 自身持券 0（i1 未交货），a=0；C 付得起 b=200；k=0 卖方有责。
	checkStep(t, res, 1, Step{ID: "i2", SD: 1, Remain: 200, SellerAvail: 0,
		BuyerAfford: 200, AfterRemain: 200, SellerLiable: true,
		FinalStatus: instr.Open})
	if b.Holdings("S1", "X") != 100 || b.Cash("C") != 1000 {
		t.Fatalf("expected no transfer, holdings=%d cash=%d", b.Holdings("S1", "X"), b.Cash("C"))
	}

	// 正向链式：i1 交 200 后 B 收到券，i2 立即可用这批货交给 C。
	b2 := instr.New(100, 0, 0, 100)
	e2 := New(b2)
	must(t, b2.SetPrice(0, "X", 10), "price2")
	must(t, b2.Credit(0, "S1", "X", 200), "credit2")
	must(t, b2.CreditCash(0, "B", 2000), "cash B2")
	must(t, b2.CreditCash(0, "C", 1000), "cash C2")
	must(t, b2.Instruct(0, "i1", "S1", "B", "X", 200, 2000, 1), "i1-2")
	must(t, b2.Instruct(0, "i2", "B", "C", "X", 200, 1000, 1), "i2-2")
	res2, err := e2.RunSettle(1)
	if err != nil {
		t.Fatal(err)
	}
	checkStep(t, res2, 0, Step{ID: "i1", SD: 1, Remain: 200, SellerAvail: 200,
		BuyerAfford: 200, Deliver: 200, Pay: 2000, AfterRemain: 0,
		FinalStatus: instr.Settled})
	checkStep(t, res2, 1, Step{ID: "i2", SD: 1, Remain: 200, SellerAvail: 200,
		BuyerAfford: 200, Deliver: 200, Pay: 1000, AfterRemain: 0,
		FinalStatus: instr.Settled})
	if b2.Holdings("C", "X") != 200 || b2.Cash("S1") != 2000 || b2.Cash("B") != 1000 {
		t.Fatalf("chain settlement wrong: Cqty=%d Scash=%d Bcash=%d",
			b2.Holdings("C", "X"), b2.Cash("S1"), b2.Cash("B"))
	}
}

// TestAgeBoundary 日龄恰等于 A 买入，小 1 不买入（且仅买方有责时取消）。
func TestAgeBoundary(t *testing.T) {
	// 仅买方有责（卖方有足够券）：age-1 仍 OPEN，age 当日 CANCELLED 无赔付。
	run := func(runDay int) (*Result, *instr.Book) {
		b := instr.New(100, 10, 5, 2)
		e := New(b)
		must(t, b.SetPrice(1, "X", 10), "p")
		must(t, b.Credit(1, "S", "X", 1000), "q")                        // 券充足，卖方永不有责
		must(t, b.Instruct(1, "i", "S", "B", "X", 1000, 100000, 2), "i") // B 没钱
		res, err := e.RunSettle(runDay)
		if err != nil {
			t.Fatal(err)
		}
		return res, b
	}
	res3, _ := run(3) // day-sd=1 < A=2
	checkStep(t, res3, 0, Step{ID: "i", SD: 2, Remain: 1000, SellerAvail: 1000,
		BuyerAfford: 0, AfterRemain: 1000, BuyerLiable: true,
		BuyerFine: 5, FinalStatus: instr.Open})
	res4, b4 := run(4) // day-sd=2 == A
	checkStep(t, res4, 0, Step{ID: "i", SD: 2, Remain: 1000, SellerAvail: 1000,
		BuyerAfford: 0, AfterRemain: 1000, BuyerLiable: true, BuyerFine: 5,
		Cancelled: true, FinalStatus: instr.Cancelled})
	ins, _ := b4.Get("i")
	if ins.Status != instr.Cancelled || b4.Payable("S") != 0 || b4.Receivable("B") != 0 {
		t.Fatalf("buyer-only cancel must have no comp: %+v", ins)
	}

	// 卖方有责且赔付为 0：market<=unpaid 时 comp=0，但仍买入。
	b := instr.New(100, 10, 5, 1)
	e := New(b)
	must(t, b.SetPrice(0, "X", 1), "p2")                           // 价 1
	must(t, b.Instruct(0, "i", "S", "B", "X", 100, 1000, 1), "i2") // S 无券
	must(t, b.CreditCash(0, "B", 100000), "cash")
	res, err := e.RunSettle(2)
	if err != nil {
		t.Fatal(err)
	}
	// r'×price=100 < amount-paid=1000 → comp=0
	checkStep(t, res, 0, Step{ID: "i", SD: 1, Remain: 100, SellerAvail: 0,
		BuyerAfford: 100, AfterRemain: 100, SellerLiable: true, SellerFine: 1,
		BuyIn: true, Comp: 0, FinalStatus: instr.BoughtIn})
}

// TestTouchedCounter touched 只数 sd<=day 的未了结指令；
// 与已了结数、未到期数无关。两档：各 10 条、各 10000 条。
func TestTouchedCounter(t *testing.T) {
	for _, n := range []int{10, 10000} {
		b := instr.New(1, 0, 0, 100)
		e := New(b)
		must(t, b.SetPrice(0, "X", 1), "p")
		must(t, b.CreditCash(0, "CASH", int64(n)), "cash")
		// n 条到期且可交收 → 了结；n 条到期不付款 → 保持未了结；n 条未到期。
		for i := 0; i < n; i++ {
			must(t, b.Credit(0, "S", "X", 1), "credit")
			must(t, b.Instruct(0, fmtID("done", i), "S", "CASH", "X", 1, 1, 1), "done")
			must(t, b.Instruct(0, fmtID("open", i), "S", fmtID("POOR", i), "X", 1, 1, 1), "open")
			must(t, b.Instruct(0, fmtID("future", i), "S", "CASH", "X", 1, 1, 100), "future")
		}
		res, err := e.RunSettle(1)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Steps) != 2*n {
			t.Fatalf("n=%d steps=%d want %d", n, len(res.Steps), 2*n)
		}
		if got := b.Touched(); got != 2*n {
			t.Fatalf("n=%d touched=%d want %d (settled=%d future=%d)",
				n, got, 2*n, n, n)
		}
		// 次日：done 已了结、future 未到期，仍只有 open 的 n 条被触碰。
		res2, err := e.RunSettle(2)
		if err != nil {
			t.Fatal(err)
		}
		if len(res2.Steps) != n || b.Touched() != n {
			t.Fatalf("n=%d day2 steps=%d touched=%d want %d",
				n, len(res2.Steps), b.Touched(), n)
		}
	}
}

func fmtID(prefix string, i int) string {
	return prefix + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
