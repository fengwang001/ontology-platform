package cancel

import (
	"fmt"
	"sync"
	"testing"
)

func testCfg() Config {
	return Config{DisputeWindow: 100, LateTolerance: 10, PrepLossBP: 3000,
		MerchantPenalty: 500, RiderComp: 200}
}

func newSvc(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func newSvcB(t testing.TB, cfg Config) *Service {
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mkOrder(id string, g, p, d, c int64, promise int64) Order {
	return Order{ID: id, Amounts: Amounts{Goods: g, Packing: p, Delivery: d, Coupon: c},
		PromiseDelivery: promise}
}

func must(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func wantCode(t *testing.T, err error, want ErrCode, ctx string) {
	t.Helper()
	if Code(err) != want {
		t.Fatalf("%s: want code %s, got %v", ctx, want.Name(), err)
	}
}

// assertConservation 校验四方守恒：四方之和 == 实付 + 优惠券面额。
func assertConservation(t *testing.T, a Amounts, l Ledger, ctx string) {
	t.Helper()
	got := l.UserRefund + l.Merchant + l.Rider + l.Platform
	want := a.Paid() + a.Coupon
	if got != want {
		t.Fatalf("%s: conservation broken: %+v sum=%d want=%d", ctx, l, got, want)
	}
}

func TestInvalidParamsAndClock(t *testing.T) {
	bad := []Config{
		{DisputeWindow: -1},
		{PrepLossBP: 10001},
		{LateTolerance: -1},
		{MerchantPenalty: -1},
		{RiderComp: -1},
	}
	for i, c := range bad {
		if _, err := NewService(c); Code(err) != ErrInvalidParam {
			t.Fatalf("case %d: want invalid_param, got %v", i, err)
		}
	}
	s := newSvc(t, testCfg())
	if err := s.CreateOrder(1, mkOrder("o", -1, 0, 0, 0, 100)); Code(err) != ErrInvalidParam {
		t.Fatalf("negative goods: %v", err)
	}
	if err := s.CreateOrder(1, mkOrder("o", 100, 0, 0, 101, 100)); Code(err) != ErrInvalidParam {
		t.Fatalf("coupon>goods: %v", err)
	}
	if err := s.CreateOrder(1, mkOrder("o", 50, 0, 10, 100, 100)); Code(err) != ErrInvalidParam {
		t.Fatalf("paid negative: %v", err)
	}
	must(t, s.CreateOrder(10, mkOrder("o", 100, 0, 0, 0, 100)), "create")
	wantCode(t, s.Accept(9, "o"), ErrClockRollback, "rollback")
	must(t, s.Accept(10, "o"), "rejected op must not advance clock")
}

func TestUserCancelBeforeAccept(t *testing.T) {
	s := newSvc(t, testCfg())
	a := mkOrder("o", 1000, 200, 300, 400, 1000).Amounts
	must(t, s.CreateOrder(1, mkOrder("o", 1000, 200, 300, 400, 1000)), "create")
	res, err := s.Cancel(2, "o", ActorUser)
	must(t, err, "cancel")
	if !res.Land || res.Liable != PartyUserNoFault || !res.Refund.FullRefund {
		t.Fatalf("want full no-fault, got %+v", res)
	}
	if res.Refund.CashRefund != a.Paid() || res.Refund.CouponRestored != 400 {
		t.Fatalf("refund split wrong: %+v", res.Refund)
	}
	assertConservation(t, a, res.Ledger, "user before accept")
}

func TestDisputeWindowRightEndpoint(t *testing.T) {
	s := newSvc(t, testCfg()) // window=100
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	must(t, s.Accept(0, "o"), "accept")
	if _, err := s.Cancel(0, "o", ActorUser); err != nil {
		t.Fatalf("open dispute: %v", err)
	}
	// 恰在右端点 deadline=100 声明 -> 声明超时；窗口同时到期，用户无责落地。
	_, err := s.Claim(100, "o")
	wantCode(t, err, ErrClaimTimeout, "claim at deadline")
	// 被拒绝操作不改变状态/时钟；通过到期推进使窗口落地，因声明未成立 -> 用户无责。
	landed0, err := s.ExpireDue(100)
	must(t, err, "expire at deadline")
	if len(landed0) != 1 || landed0[0].Liable != PartyUserNoFault {
		t.Fatalf("want 1 no-fault landing, got %+v", landed0)
	}
	snap, _ := s.Get("o")
	if !snap.Cancelled || snap.Result.Liable != PartyUserNoFault {
		t.Fatalf("want no-fault landing at deadline, got %+v", snap.Result)
	}

	// deadline-1 声明则成立 -> 到期时用户有责。
	s2 := newSvc(t, testCfg())
	must(t, s2.CreateOrder(0, mkOrder("o2", 1000, 0, 0, 0, 1000)), "create2")
	must(t, s2.Accept(0, "o2"), "accept2")
	_, _ = s2.Cancel(0, "o2", ActorUser)
	if _, err := s2.Claim(99, "o2"); err != nil {
		t.Fatalf("claim at 99: %v", err)
	}
	landed, err := s2.ExpireDue(100)
	must(t, err, "expire")
	if len(landed) != 1 || landed[0].Liable != PartyUser {
		t.Fatalf("want user-liable landing, got %+v", landed)
	}
}

func TestPickupBlockedDuringDispute(t *testing.T) {
	s := newSvc(t, testCfg())
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	must(t, s.Accept(1, "o"), "accept")
	must(t, s.Assign(2, "o", "r1"), "assign")
	_, err := s.Cancel(3, "o", ActorUser)
	must(t, err, "dispute")
	wantCode(t, s.Pickup(4, "o"), ErrCancelPending, "pickup in dispute")
	// 争议期间再次取消同样报待决。
	_, err = s.Cancel(5, "o", ActorUser)
	wantCode(t, err, ErrCancelPending, "second cancel in dispute")
}

func TestDisputeHeldSplitFloorAndRemainder(t *testing.T) {
	// goods=1000, coupon=333, loss=30% => bears=300；
	// couponToMerchant = 333*300/1000 = 99（向下取整），余数归平台。
	s := newSvc(t, testCfg())
	a := mkOrder("o", 1000, 100, 200, 333, 1000).Amounts
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 100, 200, 333, 1000)), "create")
	must(t, s.Accept(0, "o"), "accept")
	_, _ = s.Cancel(0, "o", ActorUser)
	if _, err := s.Claim(50, "o"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	landed, _ := s.ExpireDue(100)
	res := landed[0]
	if res.Refund.UserBears != 300 || res.Refund.CouponToMerchant != 99 {
		t.Fatalf("split wrong: %+v", res.Refund)
	}
	if res.Refund.CashRefund != a.Paid()-300 || res.Refund.CouponRestored != 0 {
		t.Fatalf("partial refund wrong: %+v paid=%d", res.Refund, a.Paid())
	}
	if res.Ledger.Merchant != 300-99 {
		t.Fatalf("merchant ledger wrong: %+v", res.Ledger)
	}
	assertConservation(t, a, res.Ledger, "dispute held")
}

func TestPartialRefundPaidZero(t *testing.T) {
	// goods=100, coupon=100, packing=delivery=0 => paid=0；
	// bears=30；现金退款 0-30 截断为 0；cm=100*30/100=30。
	s := newSvc(t, testCfg())
	a := mkOrder("o", 100, 0, 0, 100, 1000).Amounts
	must(t, s.CreateOrder(0, mkOrder("o", 100, 0, 0, 100, 1000)), "create")
	must(t, s.Accept(0, "o"), "accept")
	_, _ = s.Cancel(0, "o", ActorUser)
	_, _ = s.Claim(10, "o")
	landed, _ := s.ExpireDue(100)
	res := landed[0]
	if res.Refund.CashRefund != 0 || res.Refund.CouponRestored != 0 {
		t.Fatalf("paid=0 partial: %+v", res.Refund)
	}
	if res.Refund.UserBears != 30 || res.Refund.CouponToMerchant != 30 {
		t.Fatalf("bears/cm wrong: %+v", res.Refund)
	}
	assertConservation(t, a, res.Ledger, "paid zero")
}

func TestWaiveLandsNoFault(t *testing.T) {
	s := newSvc(t, testCfg())
	a := mkOrder("o", 1000, 0, 0, 0, 1000).Amounts
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	must(t, s.Accept(0, "o"), "accept")
	_, _ = s.Cancel(0, "o", ActorUser)
	res, err := s.Waive(20, "o")
	must(t, err, "waive")
	if !res.Land || res.Liable != PartyUserNoFault || !res.Refund.FullRefund {
		t.Fatalf("waive result: %+v", res)
	}
	assertConservation(t, a, res.Ledger, "waive")
}

func TestLateToleranceEqualityAndConservation(t *testing.T) {
	// promise=1000, tolerance=10：now=1010 恰等不算迟到；1011 迟到。
	s := newSvc(t, testCfg())
	a := mkOrder("o", 1000, 50, 150, 200, 1000).Amounts
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 50, 150, 200, 1000)), "create")
	must(t, s.Accept(1, "o"), "accept")
	must(t, s.Assign(2, "o", "r1"), "assign")
	must(t, s.Pickup(3, "o"), "pickup")
	wantCode(t, func() error { _, e := s.Cancel(1010, "o", ActorUser); return e }(),
		ErrNotCancellable, "equal not late")
	res, err := s.Cancel(1011, "o", ActorUser)
	must(t, err, "late cancel")
	if res.Liable != PartyPlatform || !res.Refund.FullRefund ||
		res.Refund.CashRefund != a.Paid() || res.Ledger.Rider != 200 {
		t.Fatalf("late result wrong: %+v", res)
	}
	assertConservation(t, a, res.Ledger, "late cancel")
}

func TestMerchantCancelRiderCompDifference(t *testing.T) {
	cfg := testCfg()
	a := mkOrder("o", 1000, 100, 200, 300, 1000).Amounts

	s := newSvc(t, cfg)
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 100, 200, 300, 1000)), "create")
	must(t, s.Accept(1, "o"), "accept")
	res, err := s.Cancel(2, "o", ActorMerchant)
	must(t, err, "m cancel")
	if res.Liable != PartyMerchant || res.Ledger.Rider != 0 {
		t.Fatalf("no-rider merchant cancel: %+v", res.Ledger)
	}
	assertConservation(t, a, res.Ledger, "merchant no rider")

	s2 := newSvc(t, cfg)
	must(t, s2.CreateOrder(0, mkOrder("o", 1000, 100, 200, 300, 1000)), "create2")
	must(t, s2.Accept(1, "o"), "accept2")
	must(t, s2.Assign(2, "o", "r1"), "assign2")
	res2, err := s2.Cancel(3, "o", ActorMerchant)
	must(t, err, "m cancel2")
	if res2.Ledger.Rider != 200 {
		t.Fatalf("rider comp expected: %+v", res2.Ledger)
	}
	assertConservation(t, a, res2.Ledger, "merchant with rider")
}

func TestPlatformCancelAllStages(t *testing.T) {
	cfg := testCfg()
	preps := []func(*Service, string){
		func(s *Service, id string) {},
		func(s *Service, id string) { must(t, s.Accept(1, id), "acc") },
		func(s *Service, id string) {
			must(t, s.Accept(1, id), "acc")
			must(t, s.Assign(2, id, "r"), "assign")
		},
		func(s *Service, id string) {
			must(t, s.Accept(1, id), "acc")
			must(t, s.Assign(2, id, "r"), "assign")
			must(t, s.Pickup(3, id), "pick")
		},
	}
	for i, prep := range preps {
		s := newSvc(t, cfg)
		a := mkOrder("o", 1000, 100, 200, 250, 1000).Amounts
		must(t, s.CreateOrder(0, mkOrder("o", 1000, 100, 200, 250, 1000)), "create")
		prep(s, "o")
		res, err := s.Cancel(5, "o", ActorPlatform)
		must(t, err, "p cancel")
		if res.Liable != PartyPlatform || !res.Refund.FullRefund {
			t.Fatalf("case %d platform cancel: %+v", i, res)
		}
		wantRider := int64(0)
		if i >= 2 {
			wantRider = 200
		}
		if res.Ledger.Rider != wantRider {
			t.Fatalf("case %d rider comp: got %d want %d", i, res.Ledger.Rider, wantRider)
		}
		assertConservation(t, a, res.Ledger, "platform cancel")
	}
}

func TestRiderCancelReassignThenUserCancel(t *testing.T) {
	s := newSvc(t, testCfg())
	a := mkOrder("o", 1000, 0, 0, 0, 1000).Amounts
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	must(t, s.Accept(1, "o"), "accept")
	must(t, s.Assign(2, "o", "r1"), "assign1")
	res, err := s.Cancel(3, "o", ActorRider)
	must(t, err, "rider cancel")
	if res.Land || res.Liable != PartyRider {
		t.Fatalf("rider cancel must not land: %+v", res)
	}
	snap, _ := s.Get("o")
	if snap.Stage != StageAccepted || snap.Cancelled || snap.RiderAborts != 1 {
		t.Fatalf("state after rider cancel: %+v", snap)
	}
	if s.RiderAbortCount("r1") != 1 {
		t.Fatalf("rider abort count: %d", s.RiderAbortCount("r1"))
	}
	// 改派后用户取消进入争议，窗口到期无责全额落地。
	must(t, s.Assign(4, "o", "r2"), "reassign")
	_, err = s.Cancel(5, "o", ActorUser)
	must(t, err, "user cancel after reassign")
	landed, err := s.ExpireDue(200)
	must(t, err, "expire")
	if len(landed) != 1 {
		t.Fatalf("want 1 landing, got %d", len(landed))
	}
	if landed[0].Liable != PartyUserNoFault {
		t.Fatalf("want no-fault, got %s", landed[0].Liable)
	}
	assertConservation(t, a, landed[0].Ledger, "reassign user cancel")
}

func TestRiderCancelPickedUpForbidden(t *testing.T) {
	s := newSvc(t, testCfg())
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	must(t, s.Accept(1, "o"), "accept")
	must(t, s.Assign(2, "o", "r1"), "assign")
	must(t, s.Pickup(3, "o"), "pickup")
	_, err := s.Cancel(4, "o", ActorRider)
	wantCode(t, err, ErrPickedUp, "rider after pickup")
	// 已支付阶段骑手取消属无权。
	s2 := newSvc(t, testCfg())
	must(t, s2.CreateOrder(0, mkOrder("o2", 1000, 0, 0, 0, 1000)), "create2")
	_, err = s2.Cancel(1, "o2", ActorRider)
	wantCode(t, err, ErrForbidden, "rider at paid")
}

func TestTerminalErrorsAndStageOrder(t *testing.T) {
	s := newSvc(t, testCfg())
	must(t, s.CreateOrder(0, mkOrder("o", 1000, 0, 0, 0, 1000)), "create")
	wantCode(t, s.Pickup(1, "o"), ErrStageOrder, "skip stages")
	must(t, s.Accept(1, "o"), "accept")
	must(t, s.Assign(2, "o", "r1"), "assign")
	must(t, s.Pickup(3, "o"), "pickup")
	must(t, s.Deliver(4, "o"), "deliver")
	var err error
	_, err = s.Cancel(5, "o", ActorUser)
	wantCode(t, err, ErrDelivered, "cancel delivered")
	wantCode(t, s.Deliver(6, "o"), ErrDelivered, "redeliver")
	_, err = s.Cancel(7, "missing", ActorUser)
	wantCode(t, err, ErrOrderNotFound, "missing order")

	s2 := newSvc(t, testCfg())
	must(t, s2.CreateOrder(0, mkOrder("x", 1000, 0, 0, 0, 1000)), "create x")
	_, err = s2.Cancel(1, "x", ActorMerchant)
	must(t, err, "m cancel x")
	_, err = s2.Cancel(2, "x", ActorPlatform)
	wantCode(t, err, ErrCancelled, "cancel twice")
}

func TestConcurrentSingleLanding(t *testing.T) {
	// 并发交错下，每笔订单至多落地一次取消且守恒成立。
	s := newSvc(t, testCfg())
	const n = 64
	for i := 0; i < n; i++ {
		id := "o" + itoa(i)
		// 已支付阶段：三种发起方的取消都立即落地，不存在争议窗口。
		must(t, s.CreateOrder(0, mkOrder(id, 1000, 100, 200, 300, 1000)), "create")
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := "o" + itoa(i)
		for _, actor := range []Actor{ActorMerchant, ActorPlatform, ActorUser} {
			wg.Add(1)
			go func(id string, actor Actor) {
				defer wg.Done()
				_, _ = s.Cancel(50, id, actor)
			}(id, actor)
		}
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		id := "o" + itoa(i)
		snap, ok := s.Get(id)
		if !ok || !snap.Cancelled {
			t.Fatalf("order %s not landed", id)
		}
		assertConservation(t, snap.Amounts, snap.Result.Ledger, "concurrent "+id)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// BenchmarkCancelIndependentOfTotalOrders 验证单笔取消裁决/拆分开销
// 不随平台订单总量增长：比较 1k 与 64k 存量订单下的每操作耗时。
func BenchmarkCancelIndependentOfTotalOrders(b *testing.B) {
	for _, n := range []int{1000, 64000} {
		b.Run(fmt.Sprintf("orders=%d", n), func(b *testing.B) {
			s := newSvcB(b, testCfg())
			for i := 0; i < n; i++ {
				id := itoa(i)
				if err := s.CreateOrder(0, mkOrder(id, 1000, 100, 200, 300, 100000)); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := itoa(i % n)
				if _, err := s.Cancel(int64(i+1), id, ActorPlatform); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkExpireIndependentOfTerminalOrders 验证争议到期落地开销
// 不随已终结订单数增长：先制造大量已终结订单，再批量打开/到期争议。
func BenchmarkExpireIndependentOfTerminalOrders(b *testing.B) {
	for _, n := range []int{1000, 32000} {
		b.Run(fmt.Sprintf("terminal=%d", n), func(b *testing.B) {
			s := newSvcB(b, testCfg())
			for i := 0; i < n; i++ {
				id := "t" + itoa(i)
				if err := s.CreateOrder(0, mkOrder(id, 1000, 0, 0, 0, 100000)); err != nil {
					b.Fatal(err)
				}
				if _, err := s.Cancel(0, id, ActorPlatform); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				id := fmt.Sprintf("d%d", k)
				if err := s.CreateOrder(int64(k)*300+1, mkOrder(id, 1000, 0, 0, 0, 100000)); err != nil {
					b.Fatal(err)
				}
				if err := s.Accept(int64(k)*300+2, id); err != nil {
					b.Fatal(err)
				}
				if _, err := s.Cancel(int64(k)*300+3, id, ActorUser); err != nil {
					b.Fatal(err)
				}
				if _, err := s.ExpireDue(int64(k)*300 + 3 + 101); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
