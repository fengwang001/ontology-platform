package cancel

import (
	"fmt"
	"sync"
	"testing"
)

func testParams() Params {
	return Params{
		DisputeWindow:     10,
		LateTolerance:     5,
		LossBasisPoints:   3000,
		MerchantPenalty:   500,
		RiderCompensation: 200,
	}
}

func testSpec() OrderSpec {
	return OrderSpec{
		Amounts:      Amounts{Goods: 10000, Packing: 1000, Delivery: 800, Coupon: 2000},
		PromisedTime: 100,
	}
}

func mustOK(t *testing.T, r *Result, err error) *Result {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return r
}

func mustErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustAdvance(t *testing.T, sys *System, at int64) []*Result {
	t.Helper()
	rs, err := sys.Advance(at)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	return rs
}

func errKind(err error) ErrKind {
	ce, ok := err.(*CancelError)
	if !ok {
		return 0
	}
	return ce.Kind
}

func TestInvalidParamsAndClock(t *testing.T) {
	bad := testParams()
	bad.LossBasisPoints = 10001
	if _, err := NewSystem(bad); errKind(err) != KindInvalidParam {
		t.Fatalf("want invalid param, got %v", err)
	}
	bad = testParams()
	bad.DisputeWindow = -1
	if _, err := NewSystem(bad); errKind(err) != KindInvalidParam {
		t.Fatalf("want invalid param, got %v", err)
	}

	sys, _ := NewSystem(testParams())
	_r1, _e1 := sys.CreateOrder(10, "o1", testSpec())
	mustErr(t, _e1)
	_ = _r1
	if _, err := sys.Accept(9, "o1"); errKind(err) != KindClockBack {
		t.Fatalf("want clock back, got %v", err)
	}
	badSpec := testSpec()
	badSpec.Amounts.Coupon = badSpec.Amounts.Goods + 1
	if _, err := sys.CreateOrder(11, "o2", badSpec); errKind(err) != KindInvalidParam {
		t.Fatalf("want coupon exceeds goods, got %v", err)
	}
	if _, err := sys.Accept(11, "missing"); errKind(err) != KindNoOrder {
		t.Fatalf("want no order, got %v", err)
	}
}

func TestUserCancelBeforeAccept(t *testing.T) {
	sys, _ := NewSystem(testParams())
	_r2, _e2 := sys.CreateOrder(0, "o", testSpec())
	mustErr(t, _e2)
	_ = _r2
	r, _e := sys.UserCancel(1, "o")
	mustErr(t, _e)
	if !r.Landed || r.Liable != PartyPlatform {
		t.Fatalf("landed=%v liable=%v", r.Landed, r.Liable)
	}
	a := testSpec().Amounts
	if r.Ledger.UserRefundCash != a.Paid() || r.Ledger.CouponRestored != a.Coupon {
		t.Fatalf("full refund mismatch: %+v", r.Ledger)
	}
	if !r.Ledger.Conserved(a) {
		t.Fatal("conservation broken")
	}
	if _, err := sys.UserCancel(2, "o"); errKind(err) != KindTerminal {
		t.Fatal("second cancel must be terminal")
	}
}

func TestDisputeRightEndpoint(t *testing.T) {
	sys, _ := NewSystem(testParams())
	_r3, _e3 := sys.CreateOrder(0, "o", testSpec())
	mustErr(t, _e3)
	_ = _r3
	_r4, _e4 := sys.Accept(1, "o")
	mustErr(t, _e4)
	_ = _r4
	_r4b, _e4b := sys.Assign(1, "o", "r")
	mustErr(t, _e4b)
	_ = _r4b
	r, _e := sys.UserCancel(2, "o")
	mustErr(t, _e)
	if !r.Pending || r.WindowEnd != 12 {
		t.Fatalf("want pending with end 12, got %+v", r)
	}
	if _, err := sys.ClaimPreparation(12, "o"); errKind(err) != KindWindowTimeout {
		t.Fatalf("want window timeout at endpoint, got %v", err)
	}
	if _, err := sys.Pickup(12, "o", "r"); errKind(err) != KindCancelPending {
		t.Fatalf("want pending at endpoint, got %v", err)
	}
	if _, err := sys.UserCancel(12, "o"); errKind(err) != KindCancelPending {
		t.Fatalf("want pending, got %v", err)
	}
	landed := mustAdvance(t, sys, 12)
	if len(landed) != 1 || !landed[0].Landed || landed[0].Liable != PartyMerchant {
		t.Fatalf("want expiry landing merchant liable, got %+v", landed)
	}
	if !landed[0].Ledger.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestClaimWithinWindowIsUserFault(t *testing.T) {
	sys, _ := NewSystem(testParams())
	_r5, _e5 := sys.CreateOrder(0, "o", testSpec())
	mustErr(t, _e5)
	_ = _r5
	_r6, _e6 := sys.Accept(1, "o")
	mustErr(t, _e6)
	_ = _r6
	_r7, _e7 := sys.UserCancel(2, "o")
	mustErr(t, _e7)
	_ = _r7
	r, _e := sys.ClaimPreparation(11, "o")
	mustErr(t, _e)
	if !r.Landed || r.Liable != PartyUser {
		t.Fatalf("want user liable, got %+v", r)
	}
	l := r.Ledger
	if l.UserBorne != 3000 || l.UserRefundCash != 6800 {
		t.Fatalf("split mismatch: %+v", l)
	}
	if l.CouponRestored != 0 {
		t.Fatal("coupon must not be restored")
	}
	if !l.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestCouponFloorAndRemainderToPlatform(t *testing.T) {
	sp := OrderSpec{Amounts: Amounts{Goods: 3, Packing: 0, Delivery: 0, Coupon: 2}}
	sys, _ := NewSystem(Params{DisputeWindow: 10, LateTolerance: 5,
		LossBasisPoints: 1, MerchantPenalty: 1, RiderCompensation: 1})
	_r8, _e8 := sys.CreateOrder(0, "o", sp)
	mustErr(t, _e8)
	_ = _r8
	_r9, _e9 := sys.Accept(1, "o")
	mustErr(t, _e9)
	_ = _r9
	_r10, _e10 := sys.UserCancel(2, "o")
	mustErr(t, _e10)
	_ = _r10
	r, _e := sys.ClaimPreparation(3, "o")
	mustErr(t, _e)
	if r.Ledger.UserBorne != 0 || r.Ledger.CouponConsumed != 0 || r.Ledger.CouponPlatform != 2 {
		t.Fatalf("floor mismatch: %+v", r.Ledger)
	}
	if !r.Ledger.Conserved(sp.Amounts) {
		t.Fatal("conservation broken")
	}

	sys2, _ := NewSystem(Params{DisputeWindow: 10, LateTolerance: 5,
		LossBasisPoints: 3334, MerchantPenalty: 1, RiderCompensation: 1})
	_r11, _e11 := sys2.CreateOrder(0, "o", sp)
	mustErr(t, _e11)
	_ = _r11
	_r12, _e12 := sys2.Accept(1, "o")
	mustErr(t, _e12)
	_ = _r12
	_r13, _e13 := sys2.UserCancel(2, "o")
	mustErr(t, _e13)
	_ = _r13
	r, _e14 := sys2.ClaimPreparation(3, "o")
	mustErr(t, _e14)
	if r.Ledger.UserBorne != 1 || r.Ledger.CouponConsumed != 0 || r.Ledger.CouponPlatform != 2 {
		t.Fatalf("remainder mismatch: %+v", r.Ledger)
	}
}

func TestZeroPaidPartialRefund(t *testing.T) {
	sys, _ := NewSystem(testParams())
	sp := OrderSpec{Amounts: Amounts{Goods: 1000, Packing: 0, Delivery: 0, Coupon: 1000}}
	_r14, _e14 := sys.CreateOrder(0, "o", sp)
	mustErr(t, _e14)
	_ = _r14
	_r15, _e15 := sys.Accept(1, "o")
	mustErr(t, _e15)
	_ = _r15
	_r16, _e16 := sys.UserCancel(2, "o")
	mustErr(t, _e16)
	_ = _r16
	r, _e := sys.ClaimPreparation(3, "o")
	mustErr(t, _e)
	if r.Ledger.UserRefundCash != 0 || r.Ledger.UserBorne != 300 {
		t.Fatalf("zero paid mismatch: %+v", r.Ledger)
	}
	if !r.Ledger.Conserved(sp.Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestLateToleranceBoundary(t *testing.T) {
	sys, _ := NewSystem(testParams())
	_r17, _e17 := sys.CreateOrder(0, "o", testSpec())
	mustErr(t, _e17)
	_ = _r17
	_r18, _e18 := sys.Accept(1, "o")
	mustErr(t, _e18)
	_ = _r18
	_r19, _e19 := sys.Assign(2, "o", "r1")
	mustErr(t, _e19)
	_ = _r19
	_r20, _e20 := sys.Pickup(3, "o", "r1")
	mustErr(t, _e20)
	_ = _r20
	if _, err := sys.UserCancel(105, "o"); errKind(err) != KindNotCancellable {
		t.Fatalf("want not cancellable at boundary, got %v", err)
	}
	r, _e := sys.UserCancel(106, "o")
	mustErr(t, _e)
	if !r.Landed || r.Liable != PartyPlatform {
		t.Fatalf("want platform liable when late, got %+v", r)
	}
	l := r.Ledger
	if l.UserRefundCash != 9800 || l.CouponRestored != 2000 || l.RiderGain != 200 {
		t.Fatalf("late cancel ledger mismatch: %+v", l)
	}
	if !l.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestRiderCancelReassignThenUserCancel(t *testing.T) {
	sys, _ := NewSystem(testParams())
	if _, err := sys.CreateOrder(0, "o", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Accept(1, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Assign(2, "o", "r1"); err != nil {
		t.Fatal(err)
	}
	r, err := sys.RiderCancel(3, "o", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Landed || r.Stage != StageAccepted || r.RiderCancellations != 1 {
		t.Fatalf("rider cancel must return to accepted: %+v", r)
	}
	if _, err := sys.RiderCancel(4, "o", "r2"); errKind(err) != KindUnauthorized {
		t.Fatalf("want unauthorized, got %v", err)
	}
	if _, err := sys.Assign(5, "o", "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Pickup(6, "o", "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.RiderCancel(7, "o", "r2"); errKind(err) != KindPickedUp {
		t.Fatalf("want picked up, got %v", err)
	}

	sys2, _ := NewSystem(testParams())
	if _, err := sys2.CreateOrder(0, "o2", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.Accept(1, "o2"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.Assign(2, "o2", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.RiderCancel(3, "o2", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.Assign(4, "o2", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.UserCancel(5, "o2"); err != nil {
		t.Fatal(err)
	}
	rr, err := sys2.WaiveClaim(6, "o2")
	if err != nil {
		t.Fatal(err)
	}
	if !rr.Landed || rr.Liable != PartyMerchant || rr.Ledger.RiderComp != 200 {
		t.Fatalf("after reassign compensation expected: %+v", rr)
	}
	if !rr.Ledger.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestMerchantCancelRiderCompensationDiff(t *testing.T) {
	sys, _ := NewSystem(testParams())
	if _, err := sys.CreateOrder(0, "a", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Accept(1, "a"); err != nil {
		t.Fatal(err)
	}
	r, err := sys.MerchantCancel(2, "a")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ledger.MerchantPenalty != 500 || r.Ledger.RiderComp != 0 {
		t.Fatalf("no rider: %+v", r.Ledger)
	}
	if !r.Ledger.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
	if _, err := sys.CreateOrder(3, "b", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Accept(4, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Assign(5, "b", "r"); err != nil {
		t.Fatal(err)
	}
	r, err = sys.MerchantCancel(6, "b")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ledger.RiderComp != 200 || r.Ledger.RiderGain != 200 {
		t.Fatalf("with rider: %+v", r.Ledger)
	}
	if !r.Ledger.Conserved(testSpec().Amounts) {
		t.Fatal("conservation broken")
	}
}

func TestPlatformCancelConservationEveryStage(t *testing.T) {
	setups := map[string]func(*System, string){
		"paid": func(s *System, id string) {},
		"accepted": func(s *System, id string) {
			if _, err := s.Accept(1, id); err != nil {
				t.Fatal(err)
			}
		},
		"assigned": func(s *System, id string) {
			if _, err := s.Accept(1, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Assign(2, id, "r"); err != nil {
				t.Fatal(err)
			}
		},
		"picked": func(s *System, id string) {
			if _, err := s.Accept(1, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Assign(2, id, "r"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Pickup(3, id, "r"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range setups {
		sys, _ := NewSystem(testParams())
		if _, err := sys.CreateOrder(0, name, testSpec()); err != nil {
			t.Fatal(err)
		}
		setup(sys, name)
		r, err := sys.PlatformCancel(4, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		l := r.Ledger
		wantComp := int64(0)
		if name == "assigned" || name == "picked" {
			wantComp = 200
		}
		if l.UserRefundCash != 9800 || l.CouponRestored != 2000 || l.RiderComp != wantComp {
			t.Fatalf("%s ledger mismatch: %+v", name, l)
		}
		if !l.Conserved(testSpec().Amounts) {
			t.Fatalf("%s conservation broken", name)
		}
		if _, err := sys.PlatformCancel(5, name); errKind(err) != KindTerminal {
			t.Fatalf("%s second cancel", name)
		}
	}
}

func TestDeliveredIsTerminal(t *testing.T) {
	sys, _ := NewSystem(testParams())
	if _, err := sys.CreateOrder(0, "o", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Accept(1, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Assign(2, "o", "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Pickup(3, "o", "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Deliver(4, "o", "r"); err != nil {
		t.Fatal(err)
	}
	for _, op := range []func() error{
		func() error { _, e := sys.UserCancel(5, "o"); return e },
		func() error { _, e := sys.MerchantCancel(5, "o"); return e },
		func() error { _, e := sys.PlatformCancel(5, "o"); return e },
		func() error { _, e := sys.RiderCancel(5, "o", "r"); return e },
	} {
		if errKind(op()) != KindTerminal {
			t.Fatalf("delivered order must reject cancel")
		}
	}
	if _, err := sys.Deliver(6, "o", "r"); errKind(err) != KindTerminal {
		t.Fatal("double deliver must be terminal error")
	}
}

func TestStageOrderAndNoDispute(t *testing.T) {
	sys, _ := NewSystem(testParams())
	if _, err := sys.CreateOrder(0, "o", testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Pickup(1, "o", "r"); errKind(err) != KindStageOrder {
		t.Fatalf("want stage order, got %v", err)
	}
	if _, err := sys.ClaimPreparation(1, "o"); errKind(err) != KindNoDispute {
		t.Fatalf("want no dispute, got %v", err)
	}
	if _, err := sys.WaiveClaim(1, "o"); errKind(err) != KindNoDispute {
		t.Fatalf("want no dispute, got %v", err)
	}
	if _, err := sys.Deliver(1, "x", "r"); errKind(err) != KindNoOrder {
		t.Fatalf("want no order, got %v", err)
	}
	if _, err := sys.RiderCancel(1, "o", "r"); errKind(err) != KindUnauthorized {
		t.Fatalf("want unauthorized, got %v", err)
	}
}

func TestConcurrentSingleLandingAndReplay(t *testing.T) {
	mk := func() *System {
		s, _ := NewSystem(testParams())
		if _, err := s.CreateOrder(0, "o", testSpec()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Accept(1, "o"); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for trial := 0; trial < 50; trial++ {
		sys := mk()
		var wg sync.WaitGroup
		var mu sync.Mutex
		var landed []Party
		try := func(f func() (*Result, error)) {
			defer wg.Done()
			r, err := f()
			if err == nil && r.Landed {
				mu.Lock()
				landed = append(landed, r.Liable)
				mu.Unlock()
			}
		}
		wg.Add(3)
		go try(func() (*Result, error) { return sys.MerchantCancel(2, "o") })
		go try(func() (*Result, error) { return sys.PlatformCancel(2, "o") })
		go try(func() (*Result, error) { return sys.UserCancel(2, "o") })
		wg.Wait()
		// 串行等价序可能让用户取消先得手（进入争议窗口），另两笔被拒；
		// 此时推进时钟让窗口到期，仍必须恰好补一次落地。
		if len(landed) == 0 {
			rs, err := sys.Advance(12)
			if err != nil {
				t.Fatal(err)
			}
			for _, rr := range rs {
				if rr.Landed {
					landed = append(landed, rr.Liable)
				}
			}
		}
		if len(landed) != 1 {
			t.Fatalf("trial %d: exactly one landing required, got %d", trial, len(landed))
		}
	}

	// 重放确定性：同一串行序列两次结果一致。
	seq := func() (Party, Ledger) {
		s := mk()
		if _, err := s.UserCancel(2, "o"); err != nil {
			t.Fatal(err)
		}
		r, err := s.ClaimPreparation(3, "o")
		if err != nil {
			t.Fatal(err)
		}
		return r.Liable, r.Ledger
	}
	p1, l1 := seq()
	p2, l2 := seq()
	if p1 != p2 || l1 != l2 {
		t.Fatal("replay mismatch")
	}
}

func TestExpiryCostIndependentOfClosedOrders(t *testing.T) {
	// 到期落地只触及堆顶条目；已终结订单不会被重复扫描。
	sys, _ := NewSystem(testParams())
	// 全部操作在同一单调时刻完成（等时刻合法），故 1000 个争议窗口右端点相同，
	// 到期前不会被其它操作提前 sweep。
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("o%d", i)
		if _, err := sys.CreateOrder(0, id, testSpec()); err != nil {
			t.Fatal(err)
		}
		if _, err := sys.Accept(0, id); err != nil {
			t.Fatal(err)
		}
		// 一半订单直接由商家取消（终态，且不进争议堆）。
		if i%2 == 0 {
			if _, err := sys.MerchantCancel(0, id); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := sys.UserCancel(0, id); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := sys.Advance(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1000 {
		t.Fatalf("want 1000 expiry landings, got %d", len(rs))
	}
	for _, r := range rs {
		if !r.Landed || !r.Ledger.Conserved(testSpec().Amounts) {
			t.Fatalf("expiry landing bad: %+v", r)
		}
	}
	// 再次推进不产生任何落地，也不扫描已终结订单。
	rs, _ = sys.Advance(20)
	if len(rs) != 0 {
		t.Fatalf("heap must be empty after sweep, got %d", len(rs))
	}
}

func BenchmarkCancelWithManyOrders(b *testing.B) {
	sys, _ := NewSystem(testParams())
	// 预置大量已终结订单，制造与平台订单总量无关的对照场景。
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("bulk%d", i)
		if _, err := sys.CreateOrder(0, id, testSpec()); err != nil {
			b.Fatal(err)
		}
		if _, err := sys.Accept(0, id); err != nil {
			b.Fatal(err)
		}
		if _, err := sys.MerchantCancel(0, id); err != nil {
			b.Fatal(err)
		}
	}
	target := "target"
	if _, err := sys.CreateOrder(0, target, testSpec()); err != nil {
		b.Fatal(err)
	}
	if _, err := sys.Accept(0, target); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 用未终结新单做争议取消，避免改变 target 终态：这里只测读裁决路径。
		id := fmt.Sprintf("live%d", i)
		if _, err := sys.CreateOrder(1, id, testSpec()); err != nil {
			b.Fatal(err)
		}
		if _, err := sys.Accept(1, id); err != nil {
			b.Fatal(err)
		}
		if _, err := sys.MerchantCancel(1, id); err != nil {
			b.Fatal(err)
		}
	}
}
