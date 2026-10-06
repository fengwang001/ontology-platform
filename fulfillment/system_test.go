package fulfillment

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testParams() Params {
	return Params{
		PromiseDuration:   100,
		PrepAllowance:     10,
		DispatchAllowance: 20,
		PickupAllowance:   30,
		AddressExtension:  40,
		ExtensionCap:      100,
		ClaimWindow:       50,
		Tiers: []Tier{
			{Threshold: 10, Amount: 5},
			{Threshold: 20, Amount: 15},
			{Threshold: 30, Amount: 50},
		},
	}
}

// tieParams 便于构造归因并列的参数。
func tieParams(prep, dispatch, pickup, promise, addrExt int64) Params {
	p := testParams()
	p.PrepAllowance = prep
	p.DispatchAllowance = dispatch
	p.PickupAllowance = pickup
	p.PromiseDuration = promise
	p.AddressExtension = addrExt
	return p
}

func TestAttributionTieOrder(t *testing.T) {
	// 商家=骑手=15，并列取商家。
	s := mustSys(t, tieParams(10, 20, 30, 100, 40))
	drive(t, s, "o", 0, 5, 25, 55, 115)
	v, err := s.Claim("o", 116)
	must(t, err)
	if v.Responsible != Merchant {
		t.Errorf("tie merchant/rider: got %v, want merchant", v.Responsible)
	}

	// 骑手=平台=10，并列取骑手。
	s = mustSys(t, tieParams(100, 20, 30, 200, 40))
	drive(t, s, "o", 0, 30, 40, 70, 210)
	v, err = s.Claim("o", 211)
	must(t, err)
	if v.Responsible != Rider {
		t.Errorf("tie rider/platform: got %v, want rider", v.Responsible)
	}

	// 平台=用户=40（改址发生在取货之后），并列取平台。
	s = mustSys(t, tieParams(100, 20, 30, 300, 40))
	must(t, s.Accept("o", 0))
	must(t, s.Dispatch("o", 60))
	must(t, s.MealReady("o", 70))
	must(t, s.Pickup("o", 100))
	must(t, s.ChangeAddress("o", 110))
	must(t, s.Deliver("o", 341))
	v, err = s.Claim("o", 342)
	must(t, err)
	if v.Responsible != Platform {
		t.Errorf("tie platform/user: got %v, want platform", v.Responsible)
	}

	// 全部归因量为零而延误为正 → 平台。
	s = mustSys(t, tieParams(200, 20, 200, 100, 40))
	drive(t, s, "o", 0, 5, 50, 150, 150)
	v, err = s.Claim("o", 151)
	must(t, err)
	if v.Delay != 50 || v.Responsible != Platform {
		t.Errorf("all-zero attribution: delay=%d resp=%v, want delay=50 platform", v.Delay, v.Responsible)
	}
}

func TestNegativeRemainingPromise(t *testing.T) {
	// 取货时刻晚于延展后承诺时刻：剩余承诺余量截为 0，不为负。
	s := mustSys(t, testParams()) // 承诺 100
	drive(t, s, "o", 0, 5, 10, 200, 250)
	v, err := s.Claim("o", 251)
	must(t, err)
	if v.Delay != 150 {
		t.Fatalf("delay=%d, want 150", v.Delay)
	}
	// 骑手段一 200-(10+30)=160，段二 250-(200+0)=50，骑手独占。
	if v.Responsible != Rider {
		t.Errorf("responsible=%v, want rider", v.Responsible)
	}
}

func TestWeatherAfterDeliveryNotRetroactive(t *testing.T) {
	s := mustSys(t, testParams())
	drive(t, s, "o", 0, 1, 2, 3, 150) // 承诺 100，延误 50
	must(t, s.RegisterWeather("w", 50, 200, 60, 160))
	snap, _ := s.Snapshot("o")
	if snap.Extension != 0 {
		t.Fatalf("extension=%d, want 0 (no retroactive)", snap.Extension)
	}
	v, err := s.Claim("o", 170)
	must(t, err)
	if v.Delay != 50 {
		t.Errorf("delay=%d, want 50", v.Delay)
	}
}

func TestClaimWindowRightEndpoint(t *testing.T) {
	s := mustSys(t, testParams())      // 窗口 50
	drive(t, s, "in", 0, 1, 2, 3, 130) // 承诺 100，延误 30
	// 窗口内最后时刻允许。
	if _, err := s.Claim("in", 179); err != nil {
		t.Fatalf("claim at window-1: %v", err)
	}
	drive(t, s, "out", 180, 181, 182, 183, 310) // 承诺 280，延误 30
	// 恰等于窗口右端点不允许。
	if _, err := s.Claim("out", 360); !errors.Is(err, ErrWindowExpired) {
		t.Fatalf("claim at window end: want ErrWindowExpired, got %v", err)
	}
}

func TestAutoSettleAndClaimMutualExclusion(t *testing.T) {
	s := mustSys(t, testParams())        // 最高档阈值 30，窗口 50
	drive(t, s, "auto", 0, 1, 2, 3, 200) // 承诺 100，延误 100，落入最高档
	// 窗口未结束不能自动裁决。
	if _, err := s.AutoSettle("auto", 249); !errors.Is(err, ErrWindowNotEnded) {
		t.Fatalf("auto before window end: want ErrWindowNotEnded, got %v", err)
	}
	// 自动裁决生效后，用户申请报已赔付。
	v, err := s.AutoSettle("auto", 250)
	must(t, err)
	if !v.Auto || v.Amount != 50 {
		t.Errorf("auto verdict: auto=%v amount=%d, want auto=true amount=50", v.Auto, v.Amount)
	}
	if _, err := s.Claim("auto", 251); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("claim after auto: want ErrAlreadyPaid, got %v", err)
	}
	// 用户申请在先，自动裁决报已赔付。
	drive(t, s, "user", 252, 253, 254, 255, 452) // 承诺 352，延误 100
	if _, err := s.Claim("user", 453); err != nil {
		t.Fatalf("user claim: %v", err)
	}
	if _, err := s.AutoSettle("user", 502); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("auto after claim: want ErrAlreadyPaid, got %v", err)
	}
	// 非最高档不能自动裁决。
	drive(t, s, "low", 503, 504, 505, 506, 620) // 承诺 603，延误 17
	if _, err := s.AutoSettle("low", 670); !errors.Is(err, ErrNotTopTier) {
		t.Fatalf("auto non-top-tier: want ErrNotTopTier, got %v", err)
	}
	if got := len(s.Ledger()); got != 2 {
		t.Fatalf("ledger len=%d, want 2", got)
	}
}

func TestOutOfOrderEventLeavesNoTrace(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("o", 10))
	// 乱序事件全部被拒。
	expectErr(t, s.MealReady("o", 20), ErrEventOrder)
	expectErr(t, s.Pickup("o", 20), ErrEventOrder)
	expectErr(t, s.Deliver("o", 20), ErrEventOrder)
	// 被拒绝的操作不推进时钟：更早时刻的正确事件仍可接受。
	must(t, s.Dispatch("o", 15))
	// 重复事件同样乱序。
	expectErr(t, s.Dispatch("o", 16), ErrEventOrder)
	must(t, s.MealReady("o", 20))
	must(t, s.Pickup("o", 25))
	must(t, s.Deliver("o", 30))
	snap, _ := s.Snapshot("o")
	if snap.Stage != StageDelivered {
		t.Fatalf("stage=%v, want delivered", snap.Stage)
	}
}

func TestParamChangeDoesNotAffectFrozenPromise(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("old", 0)) // 冻结承诺 100，旧档位与旧窗口
	must(t, s.Accept("old2", 0))
	p := testParams()
	p.PromiseDuration = 500
	p.ClaimWindow = 5
	p.Tiers = []Tier{{Threshold: 1, Amount: 999}}
	must(t, s.UpdateParams(p, 1))
	must(t, s.Accept("new", 2))
	oldSnap, _ := s.Snapshot("old")
	newSnap, _ := s.Snapshot("new")
	if oldSnap.Promise != 100 {
		t.Errorf("old promise=%d, want 100 (frozen)", oldSnap.Promise)
	}
	if newSnap.Promise != 502 {
		t.Errorf("new promise=%d, want 502", newSnap.Promise)
	}
	// 参数变更前接受的订单仍按冻结档位与窗口裁决。
	must(t, s.Dispatch("old2", 3))
	must(t, s.MealReady("old2", 4))
	must(t, s.Pickup("old2", 5))
	must(t, s.Deliver("old2", 115)) // 承诺 100，延误 15
	v, err := s.Claim("old2", 121)  // 冻结窗口 50 仍有效；新窗口 5 早已过期
	must(t, err)
	if v.Amount != 5 || v.Tier != 0 {
		t.Errorf("frozen tiers: amount=%d tier=%d, want 5/0", v.Amount, v.Tier)
	}
}

func TestClaimStateErrors(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("a", 0))
	// 送达前申请报未送达。
	if _, err := s.Claim("a", 1); !errors.Is(err, ErrNotDelivered) {
		t.Fatalf("claim before delivery: want ErrNotDelivered, got %v", err)
	}
	// 无延误报无延误。
	drive(t, s, "b", 2, 3, 4, 5, 50) // 承诺 102，提前送达
	if _, err := s.Claim("b", 51); !errors.Is(err, ErrNoDelay) {
		t.Fatalf("claim no delay: want ErrNoDelay, got %v", err)
	}
	// 已赔付报已赔付。
	drive(t, s, "c", 52, 53, 54, 55, 200)
	if _, err := s.Claim("c", 201); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := s.Claim("c", 202); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("second claim: want ErrAlreadyPaid, got %v", err)
	}
	// 订单不存在。
	if _, err := s.Claim("ghost", 203); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("ghost claim: want ErrOrderNotFound, got %v", err)
	}
}

func TestUserResponsibleNoPayout(t *testing.T) {
	// 改址发生在取货之后且用户归因量最大：不赔付，但裁决已作出。
	s := mustSys(t, tieParams(100, 200, 30, 300, 40))
	must(t, s.Accept("o", 0))
	must(t, s.Dispatch("o", 10))
	must(t, s.MealReady("o", 20))
	must(t, s.Pickup("o", 50))
	must(t, s.ChangeAddress("o", 60)) // 取货后改址，用户归因 40
	must(t, s.Deliver("o", 370))      // 承诺 340，延误 30，骑手段二 30 < 40
	v, err := s.Claim("o", 371)
	must(t, err)
	if v.Responsible != User {
		t.Fatalf("responsible=%v, want user", v.Responsible)
	}
	if v.Amount != 0 {
		t.Errorf("amount=%d, want 0 (user responsible)", v.Amount)
	}
	if _, err := s.Claim("o", 372); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("re-claim: want ErrAlreadyPaid, got %v", err)
	}
}

func TestConcurrentClaimsExactlyOnce(t *testing.T) {
	s := mustSys(t, testParams())
	drive(t, s, "o", 0, 1, 2, 3, 200) // 延误 100
	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Claim("o", 210)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrAlreadyPaid) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("successful claims=%d, want exactly 1", ok)
	}
	if got := len(s.Ledger()); got != 1 {
		t.Fatalf("ledger len=%d, want 1 (payout happens exactly once)", got)
	}
}

func TestConcurrentMixedOps(t *testing.T) {
	s := mustSys(t, testParams())
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("order-%d", g)
			base := int64(g * 1000)
			_ = s.Accept(id, base)
			_ = s.Dispatch(id, base+1)
			_ = s.MealReady(id, base+2)
			_ = s.Pickup(id, base+3)
			_ = s.Deliver(id, base+4)
			_, _ = s.Claim(id, base+5)
		}(g)
	}
	wg.Wait()
}

// TestAdjudicationDoesNotScan 以扫描计数证明：裁决开销不随天气事件总数
// 或订单总数增长——裁决路径对二者均为零次遍历。
func TestAdjudicationDoesNotScan(t *testing.T) {
	setup := func(p Params, n int) (*System, []string, int64) {
		s := mustSys(t, p)
		ts := int64(0)
		// 登记大量天气事件。
		for i := 0; i < 2000; i++ {
			ts++
			must(t, s.RegisterWeather(fmt.Sprintf("w%d", i), ts*10, ts*10+5, 3, ts))
		}
		// 建立大量订单并全部送达（带延误）。
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("o%d", i)
			ids = append(ids, id)
			drive(t, s, id, ts+1, ts+2, ts+3, ts+4, ts+204) // 承诺 ts+101，延误 103
			ts += 204
		}
		return s, ids, ts
	}

	t.Run("claim", func(t *testing.T) {
		p := testParams()
		p.ClaimWindow = 1 << 40 // 窗口足够大，全部订单都可申请
		s, ids, ts := setup(p, 500)
		before := s.scans
		for i := 0; i < 200; i++ {
			ts++
			if _, err := s.Claim(ids[i], ts); err != nil {
				t.Fatalf("claim %s: %v", ids[i], err)
			}
		}
		if s.scans != before {
			t.Fatalf("claim triggered %d collection scans, want 0", s.scans-before)
		}
	})

	t.Run("autosettle", func(t *testing.T) {
		s, ids, ts := setup(testParams(), 500) // 窗口 50，最高档阈值 30
		before := s.scans
		for i := 0; i < 200; i++ {
			ts++
			if _, err := s.AutoSettle(ids[i], ts); err != nil {
				t.Fatalf("autosettle %s: %v", ids[i], err)
			}
		}
		if s.scans != before {
			t.Fatalf("autosettle triggered %d collection scans, want 0", s.scans-before)
		}
	})
}

func TestExtensionCapTruncation(t *testing.T) {
	s := mustSys(t, testParams()) // 上限 100，改址延展 40
	must(t, s.Accept("o", 0))     // 原始承诺 100
	must(t, s.RegisterWeather("w1", 50, 150, 50, 1))
	must(t, s.ChangeAddress("o", 2)) // 50+40=90
	must(t, s.RegisterWeather("w2", 50, 150, 50, 3))
	snap, _ := s.Snapshot("o")
	if snap.Extension != 100 {
		t.Fatalf("extension=%d, want 100 (capped)", snap.Extension)
	}
	if snap.Promise != 200 {
		t.Fatalf("promise=%d, want 200", snap.Promise)
	}
}

func TestSecondAddressChangeRejected(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("o", 0))
	must(t, s.ChangeAddress("o", 1))
	expectErr(t, s.ChangeAddress("o", 2), ErrAddrChanged)
	// 送达后改址报事件次序错误。
	drive(t, s, "d", 10, 11, 12, 13, 14)
	expectErr(t, s.ChangeAddress("d", 15), ErrEventOrder)
}

func TestTierBoundaryEquality(t *testing.T) {
	s := mustSys(t, testParams()) // 承诺 100，档位 10/20/30
	// 延误恰等于阈值归该阈值对应档；超过最高阈值归最高档。
	cases := []struct {
		id           string
		accept       int64
		deliverDelay int64
		tier         int
		amount       int64
	}{
		{"d5", 0, 5, -1, 0},     // 未达最低档
		{"d10", 106, 10, 0, 5},  // 恰等阈值 10 → 档 0
		{"d20", 217, 20, 1, 15}, // 恰等阈值 20 → 归高档 1
		{"d25", 338, 25, 1, 15},
		{"d30", 464, 30, 2, 50},    // 恰等阈值 30 → 档 2
		{"dMax", 595, 1000, 2, 50}, // 超过最高阈值 → 最高档
	}
	for _, c := range cases {
		a := c.accept
		drive(t, s, c.id, a, a+1, a+2, a+3, a+100+c.deliverDelay)
		v, err := s.Claim(c.id, a+100+c.deliverDelay+1)
		if err != nil {
			t.Fatalf("claim %s: %v", c.id, err)
		}
		if v.Delay != c.deliverDelay {
			t.Errorf("%s delay=%d, want %d", c.id, v.Delay, c.deliverDelay)
		}
		if v.Tier != c.tier || v.Amount != c.amount {
			t.Errorf("%s tier=%d amount=%d, want tier=%d amount=%d",
				c.id, v.Tier, v.Amount, c.tier, c.amount)
		}
	}
}

func mustSys(t *testing.T, p Params) *System {
	t.Helper()
	s, err := NewSystem(p)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func expectErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want error %v, got %v", want, err)
	}
}

// drive 依次推进五个事件。
func drive(t *testing.T, s *System, id string, ts ...int64) {
	t.Helper()
	must(t, s.Accept(id, ts[0]))
	must(t, s.Dispatch(id, ts[1]))
	must(t, s.MealReady(id, ts[2]))
	must(t, s.Pickup(id, ts[3]))
	must(t, s.Deliver(id, ts[4]))
}

func TestInvalidParams(t *testing.T) {
	bad := testParams()
	bad.PromiseDuration = 0
	if _, err := NewSystem(bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	bad = testParams()
	bad.Tiers = []Tier{{Threshold: 20, Amount: 1}, {Threshold: 20, Amount: 2}}
	if _, err := NewSystem(bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("non-increasing tiers: want ErrInvalidParam, got %v", err)
	}
	bad = testParams()
	bad.Tiers = nil
	if _, err := NewSystem(bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty tiers: want ErrInvalidParam, got %v", err)
	}
}

func TestClockRollback(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("a", 10))
	expectErr(t, s.Accept("b", 9), ErrClockRollback)
	expectErr(t, s.Dispatch("a", 9), ErrClockRollback)
	// 被拒绝的操作不推进时钟：相同时刻仍可被接受。
	must(t, s.Dispatch("a", 10))
}

func TestRejectionPrecedence(t *testing.T) {
	s := mustSys(t, testParams())
	must(t, s.Accept("a", 10))
	// 参数非法先于时钟回退。
	expectErr(t, s.RegisterWeather("w", 5, 5, 1, 0), ErrInvalidParam)
	// 时钟回退先于订单不存在。
	var v Verdict
	_ = v
	if _, err := s.Claim("ghost", 5); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
	// 订单已取消先于窗口类错误（未送达）。
	must(t, s.Cancel("a", 20))
	if _, err := s.Claim("a", 21); !errors.Is(err, ErrOrderCancelled) {
		t.Fatalf("want ErrOrderCancelled, got %v", err)
	}
}

func TestWeatherIntervalLeftClosedRightOpen(t *testing.T) {
	s := mustSys(t, testParams())
	// 承诺时长 100：各订单原始承诺时刻分别为 99/100/150/200。
	must(t, s.Accept("p99", -1))
	must(t, s.Accept("p100", 0))
	// 天气区间 [100, 200)，延展 30。
	must(t, s.RegisterWeather("w1", 100, 200, 30, 1))
	must(t, s.Accept("p150", 50))
	must(t, s.Accept("p200", 100))
	cases := map[string]int64{"p99": 0, "p100": 30, "p150": 30, "p200": 0}
	for id, want := range cases {
		snap, ok := s.Snapshot(id)
		if !ok {
			t.Fatalf("order %s missing", id)
		}
		if snap.Extension != want {
			t.Errorf("order %s extension=%d, want %d", id, snap.Extension, want)
		}
	}
}
