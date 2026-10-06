package settlement_test

import (
	"errors"
	"testing"

	"ontology/settlement"
)

func mustSys(tb testing.TB, days []int64, b int, bps int64) *settlement.System {
	tb.Helper()
	s, err := settlement.NewSystem(settlement.Config{BusinessDays: days, MaxFailDays: b, PenaltyBPS: bps})
	if err != nil {
		tb.Fatalf("NewSystem: %v", err)
	}
	return s
}

func mustAdd(tb testing.TB, s *settlement.System, name string, sec map[int64]int64, cash int64) {
	tb.Helper()
	if err := s.AddAccount(name, sec, cash); err != nil {
		tb.Fatalf("AddAccount %s: %v", name, err)
	}
}

func mustReg(tb testing.TB, s *settlement.System, o settlement.Order) {
	tb.Helper()
	if err := s.RegisterOrder(o); err != nil {
		tb.Fatalf("RegisterOrder %d: %v", o.ID, err)
	}
}

func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

func dumpDecisions(t *testing.T, s *settlement.System) {
	t.Helper()
	rep, err := s.LastReport()
	if err != nil {
		return
	}
	for _, d := range rep.Decisions {
		logf(t, "  day=%d order=%d before=%d sellerAvail=%d buyerMax=%d delivered=%d after=%d failed=%v resp=%s penalty=%d forced=%v comp=%d",
			rep.Day, d.ID, d.RemainingBefore, d.SellerAvail, d.BuyerMaxQty,
			d.Delivered, d.RemainingAfter, d.Failed, d.Responsible, d.Penalty, d.ForceClosed, d.Compensation)
	}
}

func dumpAccount(t *testing.T, s *settlement.System, name string) settlement.AccountView {
	t.Helper()
	v, err := s.QueryAccount(name)
	if err != nil {
		t.Fatalf("query %s: %v", name, err)
	}
	logf(t, "  account %s: sec=%v cash=%d feePay=%d feeRecv=%d compPay=%d compRecv=%d",
		name, v.Securities, v.Cash, v.FeePayable, v.FeeReceivable, v.CompPayable, v.CompReceivable)
	return v
}

// 应交割日恰等当日：可全额交割。
func TestSettleOnDueDay(t *testing.T) {
	s := mustSys(t, []int64{1, 2}, 3, 100)
	mustAdd(t, s, "B", nil, 1000)
	mustAdd(t, s, "S", map[int64]int64{10: 5}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 5, Price: 10, SettleDay: 1})
	logf(t, "input: RunBatch(day=1, ref={10:10}); order due=1 qty=5")
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	b := dumpAccount(t, s, "B")
	sv := dumpAccount(t, s, "S")
	if b.Securities[10] != 5 || b.Cash != 950 {
		t.Fatalf("buyer wrong: %+v", b)
	}
	if sv.Securities[10] != 0 || sv.Cash != 50 {
		t.Fatalf("seller wrong: %+v", sv)
	}
	ov, _ := s.QueryOrder(1)
	if ov.Status != settlement.StatusComplete || ov.DeliveredQty != 5 {
		t.Fatalf("order = %+v", ov)
	}
}

// 部分交割（允许）与整笔失败（不允许）的差别。
func TestPartialVsWholeFail(t *testing.T) {
	s := mustSys(t, []int64{1}, 5, 100)
	mustAdd(t, s, "B", nil, 1000)
	mustAdd(t, s, "S", map[int64]int64{10: 3}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 5, Price: 10, SettleDay: 1, AllowPartial: true})
	mustReg(t, s, settlement.Order{ID: 2, Security: 10, Buyer: "B", Seller: "S", Qty: 2, Price: 10, SettleDay: 1, AllowPartial: false})
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	o1, _ := s.QueryOrder(1)
	o2, _ := s.QueryOrder(2)
	if o1.DeliveredQty != 3 || o1.Status != settlement.StatusPartial {
		t.Fatalf("o1 = %+v", o1)
	}
	if o2.DeliveredQty != 0 || o2.Status != settlement.StatusPending || o2.FailedDays != 1 {
		t.Fatalf("o2 = %+v", o2)
	}
	sv, _ := s.QueryAccount("S")
	if sv.Securities[10] != 0 {
		t.Fatalf("seller sec = %d", sv.Securities[10])
	}
}

// 券款两边都不足时责任方归卖方。
func TestBothShortSellerResponsible(t *testing.T) {
	s := mustSys(t, []int64{1}, 5, 100)
	mustAdd(t, s, "B", nil, 5)
	mustAdd(t, s, "S", map[int64]int64{10: 0}, 0)
	mustReg(t, s, settlement.Order{ID: 7, Security: 10, Buyer: "B", Seller: "S", Qty: 2, Price: 10, SettleDay: 1, AllowPartial: true})
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	rep, _ := s.LastReport()
	if rep.Decisions[0].Responsible != "seller" {
		t.Fatalf("responsible = %q", rep.Decisions[0].Responsible)
	}
	sv, _ := s.QueryAccount("S")
	if sv.FeePayable == 0 {
		t.Fatalf("seller penalty missing: %+v", sv)
	}
}

// 买方现金不足：责任方为买方。
func TestBuyerShortResponsible(t *testing.T) {
	s := mustSys(t, []int64{1}, 5, 100)
	mustAdd(t, s, "B", nil, 15)
	mustAdd(t, s, "S", map[int64]int64{10: 10}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 3, Price: 10, SettleDay: 1, AllowPartial: true})
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	rep, _ := s.LastReport()
	if rep.Decisions[0].Responsible != "buyer" {
		t.Fatalf("responsible = %q", rep.Decisions[0].Responsible)
	}
	bv, _ := s.QueryAccount("B")
	if bv.FeePayable == 0 || bv.Securities[10] != 1 || bv.Cash != 5 {
		t.Fatalf("buyer = %+v", bv)
	}
}

// 同批累计占用：早先指令先占用券；收到的券次日才可用。
func TestBatchOccupationAndNextDayAvailability(t *testing.T) {
	s := mustSys(t, []int64{1, 2}, 5, 100)
	mustAdd(t, s, "B1", nil, 1000)
	mustAdd(t, s, "B2", nil, 1000)
	mustAdd(t, s, "S", map[int64]int64{10: 8}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B1", Seller: "S", Qty: 4, Price: 10, SettleDay: 1, AllowPartial: true})
	mustReg(t, s, settlement.Order{ID: 2, Security: 10, Buyer: "B2", Seller: "S", Qty: 4, Price: 10, SettleDay: 2, AllowPartial: false})
	// B1 当日收到的券不能同日转卖。
	mustReg(t, s, settlement.Order{ID: 3, Security: 10, Buyer: "B2", Seller: "B1", Qty: 1, Price: 10, SettleDay: 1, AllowPartial: false})
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	o3, _ := s.QueryOrder(3)
	if o3.DeliveredQty != 0 {
		t.Fatalf("received securities must not be usable same day, got %+v", o3)
	}
	if err := s.RunBatch(2, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	o2, _ := s.QueryOrder(2)
	o3v, _ := s.QueryOrder(3)
	if o2.DeliveredQty != 4 {
		t.Fatalf("o2 day2 = %+v", o2)
	}
	if o3v.DeliveredQty != 1 {
		t.Fatalf("o3 day2 = %+v", o3v)
	}
}

// 失败日数恰达 B：当日罚金计入后强制了结。
func TestForceCloseExactlyAtB(t *testing.T) {
	s := mustSys(t, []int64{1, 2, 3}, 2, 100)
	mustAdd(t, s, "B", nil, 1000)
	mustAdd(t, s, "S", map[int64]int64{10: 0}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 2, Price: 10, SettleDay: 1, AllowPartial: true})
	for _, d := range []int64{1, 2} {
		if err := s.RunBatch(d, map[int64]int64{10: 20}); err != nil {
			t.Fatal(err)
		}
		dumpDecisions(t, s)
	}
	o, _ := s.QueryOrder(1)
	if o.Status != settlement.StatusForceClosed || o.FailedDays != 2 || o.DeliveredQty != 0 {
		t.Fatalf("order = %+v", o)
	}
	sv, _ := s.QueryAccount("S")
	bv, _ := s.QueryAccount("B")
	if sv.FeePayable != 2 || bv.FeeReceivable != 2 {
		t.Fatalf("fees seller=%+v buyer=%+v", sv, bv)
	}
	if sv.CompPayable != 20 || bv.CompReceivable != 20 {
		t.Fatalf("comp seller=%d buyer=%d", sv.CompPayable, bv.CompReceivable)
	}
}

// 补偿为负取零；B=1 当日即强制了结。
func TestCompensationNegativeClampedZero(t *testing.T) {
	s := mustSys(t, []int64{1}, 1, 100)
	mustAdd(t, s, "B", nil, 1000)
	mustAdd(t, s, "S", map[int64]int64{10: 0}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 2, Price: 50, SettleDay: 1})
	if err := s.RunBatch(1, map[int64]int64{10: 10}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	sv, _ := s.QueryAccount("S")
	if sv.CompPayable != 0 {
		t.Fatalf("comp = %d, want 0", sv.CompPayable)
	}
	o, _ := s.QueryOrder(1)
	if o.Status != settlement.StatusForceClosed {
		t.Fatalf("status = %v", o.Status)
	}
}

// 非营业日 / 跳过 / 重复 / 倒退。
func TestDayAndOrderingErrors(t *testing.T) {
	s := mustSys(t, []int64{1, 3}, 3, 100)
	if err := s.RunBatch(2, map[int64]int64{}); !errors.Is(err, settlement.ErrNonBusinessDay) {
		t.Fatalf("non-biz = %v", err)
	}
	if err := s.RunBatch(3, map[int64]int64{}); !errors.Is(err, settlement.ErrOrdering) {
		t.Fatalf("skip first day = %v", err)
	}
	if err := s.RunBatch(1, map[int64]int64{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBatch(1, map[int64]int64{}); !errors.Is(err, settlement.ErrOrdering) {
		t.Fatalf("repeat = %v", err)
	}
	if err := s.RunBatch(3, map[int64]int64{}); err != nil {
		t.Fatalf("next biz day: %v", err)
	}
	if err := s.RunBatch(1, map[int64]int64{}); !errors.Is(err, settlement.ErrOrdering) {
		t.Fatalf("backward = %v", err)
	}
}

// 参数非法优先于非营业日与次序错误。
func TestErrorPriority(t *testing.T) {
	s := mustSys(t, []int64{1}, 3, 100)
	if err := s.RunBatch(0, nil); !errors.Is(err, settlement.ErrInvalidParam) {
		t.Fatalf("day=0: %v", err)
	}
	if err := s.RunBatch(9, map[int64]int64{1: 0}); !errors.Is(err, settlement.ErrInvalidParam) {
		t.Fatalf("bad price on non-biz day should still be invalid param: %v", err)
	}
}

// 登记错误优先级：参数非法 > 编号重复 > 账户不存在 > 日期已过；被拒操作不留痕。
func TestRegisterErrorsAndNoTrace(t *testing.T) {
	s := mustSys(t, []int64{1, 2}, 3, 100)
	mustAdd(t, s, "B", nil, 100)
	mustAdd(t, s, "S", map[int64]int64{10: 1}, 0)
	bad := settlement.Order{ID: 0, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 1}
	if err := s.RegisterOrder(bad); !errors.Is(err, settlement.ErrInvalidParam) {
		t.Fatalf("bad param: %v", err)
	}
	good := settlement.Order{ID: 5, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 1}
	mustReg(t, s, good)
	dupBad := good
	dupBad.Buyer = "ghost" // 即使账户不存在，也应先报编号重复
	if err := s.RegisterOrder(dupBad); !errors.Is(err, settlement.ErrDuplicateID) {
		t.Fatalf("dup: %v", err)
	}
	missing := settlement.Order{ID: 6, Security: 10, Buyer: "B", Seller: "NOPE", Qty: 1, Price: 1, SettleDay: 1}
	if err := s.RegisterOrder(missing); !errors.Is(err, settlement.ErrAccountMissing) {
		t.Fatalf("missing account: %v", err)
	}
	notBiz := settlement.Order{ID: 7, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 9}
	if err := s.RegisterOrder(notBiz); !errors.Is(err, settlement.ErrInvalidParam) {
		t.Fatalf("non-biz settle day: %v", err)
	}
	// 完成 day1 批处理后登记应交割日 1 的指令 => 日期已过。
	if err := s.RunBatch(1, map[int64]int64{10: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.RunBatch(2, map[int64]int64{10: 1}); err != nil {
		t.Fatal(err)
	}
	passed := settlement.Order{ID: 8, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 1}
	if err := s.RegisterOrder(passed); !errors.Is(err, settlement.ErrDatePassed) {
		t.Fatalf("date passed: %v", err)
	}
	if _, err := s.QueryOrder(8); !errors.Is(err, settlement.ErrOrderNotFound) {
		t.Fatalf("rejected order must leave no trace: %v", err)
	}
	if _, err := s.QueryOrder(0); !errors.Is(err, settlement.ErrInvalidParam) {
		t.Fatalf("query invalid id: %v", err)
	}
	// day1 拒绝登记不得改变头寸：S 已交割 1 件给 B。
	sv := dumpAccount(t, s, "S")
	bv := dumpAccount(t, s, "B")
	if sv.Securities[10] != 0 || sv.Cash != 1 || bv.Securities[10] != 1 || bv.Cash != 99 {
		t.Fatalf("state mutated by rejected ops: %+v %+v", sv, bv)
	}
}

// 罚金向上取整：ceil 验证。
func TestPenaltyCeil(t *testing.T) {
	s := mustSys(t, []int64{1}, 1, 1) // 1 bp，B=1
	mustAdd(t, s, "B", nil, 0)
	mustAdd(t, s, "S", map[int64]int64{10: 0}, 0)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 5, SettleDay: 1})
	if err := s.RunBatch(1, map[int64]int64{10: 5}); err != nil {
		t.Fatal(err)
	}
	dumpDecisions(t, s)
	sv, _ := s.QueryAccount("S")
	// ceil(5 * 1 / 10000) = 1。
	if sv.FeePayable != 1 {
		t.Fatalf("penalty = %d, want 1", sv.FeePayable)
	}
}

// 罚金/补偿只记应付应收，不改变现金；现金与券总量守恒。
func TestConservation(t *testing.T) {
	s := mustSys(t, []int64{1, 2}, 1, 500)
	mustAdd(t, s, "B", nil, 100)
	mustAdd(t, s, "S", map[int64]int64{10: 0}, 30)
	mustReg(t, s, settlement.Order{ID: 1, Security: 10, Buyer: "B", Seller: "S", Qty: 4, Price: 20, SettleDay: 1, AllowPartial: true})
	tot := func() (int64, int64) {
		var q, c int64
		for _, n := range []string{"B", "S"} {
			v, _ := s.QueryAccount(n)
			q += v.Securities[10]
			c += v.Cash
		}
		return q, c
	}
	q0, c0 := tot()
	if err := s.RunBatch(1, map[int64]int64{10: 100}); err != nil {
		t.Fatal(err)
	}
	q1, c1 := tot()
	if q1 != q0 || c1 != c0 {
		t.Fatalf("not conserved after batch: sec %d->%d cash %d->%d", q0, q1, c0, c1)
	}
	dumpDecisions(t, s)
	dumpAccount(t, s, "B")
	dumpAccount(t, s, "S")
	if err := s.RunBatch(2, map[int64]int64{10: 100}); err != nil {
		t.Fatal(err)
	}
	q2, c2 := tot()
	if q2 != q0 || c2 != c0 {
		t.Fatalf("not conserved after day2: sec %d->%d cash %d->%d", q0, q2, c0, c2)
	}
}
