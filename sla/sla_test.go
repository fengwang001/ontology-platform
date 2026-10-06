package sla

import (
	"errors"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		PromiseDuration:  60,
		MerchantPrep:     20,
		PlatformDispatch: 10,
		RiderPickup:      10,
		UserAddrExtend:   15,
		ExtendCap:        100,
		ClaimWindow:      30,
		TierThresholds:   []int64{10, 30, 60},
		TierPayouts:      []int64{5, 15, 40},
	}
}

// 剩余承诺余量为负（取货晚于延展后承诺）时按 0 处理。
func TestNegativeRemaining(t *testing.T) {
	cfg := testConfig()
	cfg.RiderPickup = 0
	s := newSystem(cfg)
	drive(s, "a", 0, 5, 10, 80, 100) // 承诺60：取货超 ready 70，送达段 100-80=20 → rider90
	r, err := s.Claim("a", 105)
	panicIf(err)
	if r.RiderBlame != 90 || r.Delay != 40 || r.Party != PartyRider {
		t.Fatalf("%+v", r)
	}
}

// 全部归因量为零而延误为正 → 平台。
// 取货时刻晚于承诺，deliver=pickup：延误落在“承诺→取货”空隙，
// 宽松容许下各分段均为 0。
func TestZeroBlameGoesPlatform(t *testing.T) {
	cfg := testConfig()
	cfg.MerchantPrep = 100
	cfg.PlatformDispatch = 100
	cfg.RiderPickup = 100
	s := newSystem(cfg)
	drive(s, "a", 0, 0, 0, 70, 70)
	r, err := s.Claim("a", 75)
	panicIf(err)
	if r.Party != PartyPlatform || r.MerchantBlame != 0 || r.RiderBlame != 0 ||
		r.PlatformBlame != 0 || r.UserBlame != 0 || r.Payout != 5 {
		t.Fatalf("%+v", r)
	}
}

// 用户为责任方时不赔付，但仍记账且后续报已赔付。
func TestUserFaultNoPayout(t *testing.T) {
	cfg := testConfig()
	cfg.UserAddrExtend = 10
	cfg.ExtendCap = 0 // 改址延展被截断为 0，承诺不延后；用户段仍记固定延展量
	s := newSystem(cfg)
	panicIf(s.Accept("a", 0))
	panicIf(s.Dispatch("a", 0))
	panicIf(s.Ready("a", 0))
	panicIf(s.Pickup("a", 0))
	panicIf(s.ChangeAddress("a", 5))
	panicIf(s.Deliver("a", 65)) // rider 送达段 5；user 10 → 用户责任
	r, err := s.Claim("a", 70)
	panicIf(err)
	if r.Party != PartyUser || r.Payout != 0 || r.Delay != 5 {
		t.Fatalf("user fault: %+v", r)
	}
	mustCode(t, rulingErr(s.Claim("a", 71)), CodeAlreadyPaid)
	if len(s.Ledger()) != 1 || s.Ledger()[0].Amount != 0 {
		t.Fatalf("zero-payout ruling must still be booked once")
	}
}

// 送达后登记的天气事件不追溯。
func TestWeatherAfterDeliveryIgnored(t *testing.T) {
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 90)
	panicIf(s.AddWeather("w", 0, 300, 50))
	r, _ := s.RulingOf("a")
	if r.ExtendedPromise != 60 || r.Delay != 30 {
		t.Fatalf("weather after delivery must not apply: %+v", r)
	}
	rr, err := s.Claim("a", 95)
	panicIf(err)
	if rr.Payout != 15 {
		t.Fatalf("got %d", rr.Payout)
	}
}

// 申请窗口右端点不允许；送达前申请报未送达。
func TestClaimWindow(t *testing.T) {
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 90) // 右端点 120
	mustCode(t, rulingErr(s.Claim("a", 120)), CodeWindowClosed)
	mustCode(t, rulingErr(s.Claim("a", 121)), CodeWindowClosed)
	r, err := s.Claim("a", 119)
	panicIf(err)
	if r.Payout != 15 {
		t.Fatalf("got %d", r.Payout)
	}
	mustCode(t, rulingErr(s.Claim("a", 119)), CodeAlreadyPaid)

	s2 := newSystem(testConfig())
	panicIf(s2.Accept("b", 0))
	mustCode(t, rulingErr(s2.Claim("b", 5)), CodeNotDelivered)
}

// 无延误拒绝且不留痕。
func TestNoDelay(t *testing.T) {
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 55)
	mustCode(t, rulingErr(s.Claim("a", 58)), CodeNoDelay)
	mustCode(t, rulingErr(s.Claim("a", 59)), CodeNoDelay)
	if len(s.Ledger()) != 0 {
		t.Fatal("rejected claim must not be booked")
	}
}

// 取消订单：事件与申请报已取消；送达后不可取消。
func TestCanceled(t *testing.T) {
	s := newSystem(testConfig())
	panicIf(s.Accept("a", 0))
	panicIf(s.Cancel("a"))
	mustCode(t, s.Dispatch("a", 5), CodeOrderCanceled)
	mustCode(t, rulingErr(s.Claim("a", 100)), CodeOrderCanceled)

	s2 := newSystem(testConfig())
	drive(s2, "b", 0, 5, 10, 12, 90)
	mustCode(t, s2.Cancel("b"), CodeEventOrder)
}

// 乱序事件被拒不留痕：之后按正确次序仍可正常完成。
func TestEventOrderNoTrace(t *testing.T) {
	s := newSystem(testConfig())
	panicIf(s.Accept("a", 0))
	mustCode(t, s.Ready("a", 5), CodeEventOrder)
	mustCode(t, s.Deliver("a", 5), CodeEventOrder)
	mustCode(t, s.Pickup("a", 5), CodeEventOrder)
	panicIf(s.Dispatch("a", 5))
	mustCode(t, s.Deliver("a", 6), CodeEventOrder)
	panicIf(s.Ready("a", 10))
	mustCode(t, s.Deliver("a", 11), CodeEventOrder)
	panicIf(s.Pickup("a", 12))
	panicIf(s.Deliver("a", 90))
	r, err := s.Claim("a", 95)
	panicIf(err)
	if r.Delay != 30 {
		t.Fatalf("rejected events must leave no trace, delay=%d", r.Delay)
	}
	mustCode(t, s.Deliver("a", 91), CodeEventOrder)
}

// 时钟回退：被拒接受不推进时钟。
func TestClockRollback(t *testing.T) {
	s := newSystem(testConfig())
	panicIf(s.Accept("a", 100))
	mustCode(t, s.Accept("b", 99), CodeClockRollback)
	mustCode(t, s.Accept("b", 50), CodeClockRollback)
	panicIf(s.Accept("b", 100))
	panicIf(s.Accept("c", 200))
}

// 参数非法的各类构造配置。
func TestInvalidParams(t *testing.T) {
	bad := []Config{
		{},
		{PromiseDuration: 10, TierThresholds: []int64{1, 1}, TierPayouts: []int64{1, 2}},
		{PromiseDuration: 10, TierThresholds: []int64{2, 1}, TierPayouts: []int64{1, 2}},
		{PromiseDuration: 10, TierThresholds: []int64{1}, TierPayouts: []int64{1, 2}},
		{PromiseDuration: -1, TierThresholds: []int64{1}, TierPayouts: []int64{1}},
	}
	for i, c := range bad {
		if _, err := NewChecked(c); err == nil {
			t.Fatalf("case %d should be invalid", i)
		}
	}
	if _, err := NewChecked(testConfig()); err != nil {
		t.Fatal(err)
	}
}

// 自动裁决与用户申请互斥；自动裁决仅最高档且窗口结束后。
func TestAutoAdjudication(t *testing.T) {
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 130) // delay70 最高档
	// 窗口右端点 = deliver 130 + 30 = 160。
	mustCode(t, rulingErr(s.AutoAdjudicate("a", 159)), CodeWindowClosed)
	mustCode(t, rulingErr(s.AutoAdjudicate("missing", 1000)), CodeOrderNotFound)
	r, err := s.AutoAdjudicate("a", 160)
	panicIf(err)
	if !r.Automatic || r.Payout != 40 {
		t.Fatalf("auto: %+v", r)
	}
	mustCode(t, rulingErr(s.Claim("a", 159)), CodeAlreadyPaid)
	mustCode(t, rulingErr(s.AutoAdjudicate("a", 161)), CodeAlreadyPaid)

	// 非最高档订单不可自动裁决。
	s2 := newSystem(testConfig())
	drive(s2, "b", 0, 5, 10, 12, 90) // delay30 第二档
	mustCode(t, rulingErr(s2.AutoAdjudicate("b", 120)), CodeInvalidParam)

	// 用户申请先到 → 自动裁决被已赔付挡回。
	s3 := newSystem(testConfig())
	drive(s3, "c", 0, 5, 10, 12, 130)
	_, err = s3.Claim("c", 140)
	panicIf(err)
	mustCode(t, rulingErr(s3.AutoAdjudicate("c", 160)), CodeAlreadyPaid)
}

// 参数变更不影响已接受订单的冻结承诺与档位表。
func TestConfigChangeFreeze(t *testing.T) {
	s, err := NewChecked(testConfig())
	panicIf(err)
	panicIf(s.Accept("a", 0)) // promised 60，档位 {10,30,60}
	newCfg := testConfig()
	newCfg.PromiseDuration = 300
	newCfg.TierThresholds = []int64{1}
	newCfg.TierPayouts = []int64{999}
	panicIf(s.UpdateConfig(newCfg))
	panicIf(s.Dispatch("a", 5))
	panicIf(s.Ready("a", 10))
	panicIf(s.Pickup("a", 12))
	panicIf(s.Deliver("a", 90))
	r, err := s.Claim("a", 95)
	panicIf(err)
	if r.PromisedAt != 60 || r.Payout != 15 {
		t.Fatalf("frozen promise/tier violated: %+v", r)
	}
	// 新订单使用新参数。
	panicIf(s.Accept("b", 0)) // promised 300
	rb, _ := s.RulingOf("b")
	if rb.PromisedAt != 300 {
		t.Fatalf("new order should use new config: %d", rb.PromisedAt)
	}
}

// 并发：同一订单事件次序在并行交错下保持；赔付恰好一次。
func TestConcurrentOrdering(t *testing.T) {
	s := newSystem(testConfig())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := "o" + itoa(g)
			// 每个协程对自己的订单乱序并发打事件，只期望恰好一条完整链成功。
			try := func(fn func() error) {
				_ = fn()
			}
			try(func() error { return s.Accept(id, 0) })
			try(func() error { return s.Dispatch(id, 0) })
			try(func() error { return s.Ready(id, 0) })
			try(func() error { return s.Pickup(id, 0) })
			try(func() error { return s.Deliver(id, 90+int64(g)) })
		}(g)
	}
	wg.Wait()

	// 高竞争：同一订单全部事件由多协程重复并发调用，最终必为已送达且可裁决一次。
	s2 := newSystem(testConfig())
	var wg2 sync.WaitGroup
	ops := []func() error{
		func() error { return s2.Accept("hot", 0) },
		func() error { return s2.Dispatch("hot", 0) },
		func() error { return s2.Ready("hot", 0) },
		func() error { return s2.Pickup("hot", 0) },
		func() error { return s2.Deliver("hot", 90) },
	}
	for rep := 0; rep < 20; rep++ {
		for _, op := range ops {
			wg2.Add(1)
			go func(fn func() error) { defer wg2.Done(); _ = fn() }(op)
		}
	}
	wg2.Wait()
	var winners int
	var wmu sync.Mutex
	for i := 0; i < 50; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if _, err := s2.Claim("hot", 95); err == nil {
				wmu.Lock()
				winners++
				wmu.Unlock()
			}
		}()
	}
	wg2.Wait()
	if winners != 1 || len(s2.Ledger()) != 1 {
		t.Fatalf("exactly-once violated: winners=%d ledger=%d", winners, len(s2.Ledger()))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// 归因并列次序：商家 > 骑手 > 平台 > 用户。
func TestBlameTieOrder(t *testing.T) {
	cfg := testConfig()
	cfg.PromiseDuration = 100
	cfg.MerchantPrep = 0
	cfg.PlatformDispatch = 0
	cfg.RiderPickup = 0

	// 商家 20 vs 骑手 20，delay 20：并列取商家。
	// accept0 dispatch0 ready20 pickup40 deliver120：
	// merchant=20；取货超 ready 20；余量 100-40=60，送达段 120-100=20 → rider40 不并列。
	// 取 pickup30：取货超 10，送达段 120-(30+70)=20 → rider30。
	// 取 deliver110：余量70，送达段 10，取货超10 → rider20，delay10。
	s := newSystem(cfg)
	drive(s, "a", 0, 0, 20, 30, 110)
	r, err := s.Claim("a", 115)
	panicIf(err)
	if r.Party != PartyMerchant || r.MerchantBlame != 20 || r.RiderBlame != 20 {
		t.Fatalf("merchant/rider tie: %+v", r)
	}

	// 骑手 10 vs 平台 10（商家段为 0）：并列取骑手。
	// 派单容许 0 → 平台 10；出餐容许 20 → 商家 0；
	// 取货容许 10，pickup20-ready10=10 不超 → 取货段 0；
	// 余量 100-20=80，deliver110 → 送达段 110-(20+80)=10。
	cfgRP := testConfig()
	cfgRP.PromiseDuration = 100
	cfgRP.PlatformDispatch = 0
	cfgRP.MerchantPrep = 20
	s2 := newSystem(cfgRP)
	drive(s2, "b", 0, 10, 10, 20, 110)
	r2, err := s2.Claim("b", 115)
	panicIf(err)
	if r2.Party != PartyRider || r2.PlatformBlame != 10 || r2.RiderBlame != 10 {
		t.Fatalf("rider/platform tie: %+v", r2)
	}

	// 平台 10 vs 用户 10（骑手段为 0）：并列取平台。
	cfg2 := cfg
	cfg2.UserAddrExtend = 10
	cfg2.MerchantPrep = 20
	s3 := newSystem(cfg2)
	panicIf(s3.Accept("c", 0))
	panicIf(s3.Dispatch("c", 10)) // platform 10
	panicIf(s3.Ready("c", 10))
	panicIf(s3.Pickup("c", 10))
	panicIf(s3.ChangeAddress("c", 15)) // 取货后改址：用户段 10，承诺 110
	panicIf(s3.Deliver("c", 111))      // rider 送达段 1；delay=1
	r3, err := s3.Claim("c", 115)
	panicIf(err)
	if r3.Party != PartyPlatform || r3.PlatformBlame != 10 || r3.UserBlame != 10 || r3.RiderBlame != 1 {
		t.Fatalf("platform/user tie: %+v", r3)
	}
}

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func mustCode(t *testing.T, err error, want Code) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("want sla.Error, got %T: %v", err, err)
	}
	if se.Code != want {
		t.Fatalf("want code %d, got %d (%v)", want, se.Code, err)
	}
}

func rulingErr(_ *Ruling, err error) error { return err }

func drive(s *System, id string, accept, dispatch, ready, pickup, deliver int64) {
	panicIf(s.Accept(id, accept))
	panicIf(s.Dispatch(id, dispatch))
	panicIf(s.Ready(id, ready))
	panicIf(s.Pickup(id, pickup))
	panicIf(s.Deliver(id, deliver))
}

// 天气区间左闭右开：左端点覆盖，恰在右端点不获得。
func TestWeatherHalfOpen(t *testing.T) {
	s := newSystem(testConfig())
	panicIf(s.AddWeather("w", 160, 200, 20))
	panicIf(s.Accept("a", 100)) // promisedAt=160，恰为左端点 → 覆盖
	panicIf(s.Dispatch("a", 105))
	panicIf(s.Ready("a", 115))
	panicIf(s.Pickup("a", 120))
	panicIf(s.Deliver("a", 230))
	r, err := s.RulingOf("a")
	panicIf(err)
	if r.ExtendedPromise != 180 || r.Delay != 50 {
		t.Fatalf("left-closed: got promise=%d delay=%d", r.ExtendedPromise, r.Delay)
	}

	s2 := newSystem(testConfig())
	panicIf(s2.AddWeather("w", 160, 200, 20))
	panicIf(s2.Accept("b", 140)) // promisedAt=200，恰为右端点 → 不覆盖
	panicIf(s2.Dispatch("b", 145))
	panicIf(s2.Ready("b", 155))
	panicIf(s2.Pickup("b", 160))
	panicIf(s2.Deliver("b", 260))
	r2, _ := s2.RulingOf("b")
	if r2.ExtendedPromise != 200 || r2.Delay != 60 {
		t.Fatalf("right endpoint must be excluded, got %+v", r2)
	}
}

// 同一天气事件不重复生效；重复 ID 拒绝。
func TestWeatherDedup(t *testing.T) {
	s := newSystem(testConfig())
	panicIf(s.Accept("a", 100)) // promised 160
	panicIf(s.AddWeather("w1", 0, 160, 7))
	mustCode(t, s.AddWeather("w1", 1, 2, 3), CodeInvalidParam)
	r0, _ := s.RulingOf("a")
	if r0.ExtendedPromise != 160 {
		t.Fatalf("right endpoint excluded: %d", r0.ExtendedPromise)
	}
	panicIf(s.AddWeather("w2", 160, 161, 9))
	r1, _ := s.RulingOf("a")
	if r1.ExtendedPromise != 169 {
		t.Fatalf("left-closed coverage: %d", r1.ExtendedPromise)
	}
}

// 延展累计上限截断（天气 + 改址）。
func TestExtendCap(t *testing.T) {
	s := newSystem(testConfig()) // cap=100, 改址=15
	panicIf(s.Accept("a", 0))    // promised 60
	panicIf(s.AddWeather("w1", 0, 200, 80))
	panicIf(s.AddWeather("w2", 0, 200, 70)) // 天气合计 150
	panicIf(s.ChangeAddress("a", 50))       // +15 → 165，截断到 100
	panicIf(s.Dispatch("a", 5))
	panicIf(s.Ready("a", 10))
	panicIf(s.Pickup("a", 12))
	panicIf(s.Deliver("a", 200))
	r, _ := s.RulingOf("a")
	if r.ExtendedPromise != 160 {
		t.Fatalf("cap truncation: got %d", r.ExtendedPromise)
	}
}

// 再次改址报已改址；送达后改址报事件次序错误（状态优先于已改址之外的判定）。
func TestReaddressTwice(t *testing.T) {
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 90)
	mustCode(t, s.ChangeAddress("a", 95), CodeEventOrder)

	s2 := newSystem(testConfig())
	panicIf(s2.Accept("b", 0))
	panicIf(s2.ChangeAddress("b", 5))
	mustCode(t, s2.ChangeAddress("b", 6), CodeAlreadyReaddressed)
}

// 档位阈值取等归高档；超最高归最高档。
func TestTierEqualityHigh(t *testing.T) {
	cases := []struct {
		deliver int64
		payout  int64
	}{
		{70, 5},   // delay 10 → 第一档
		{90, 15},  // delay 30 → 第二档（非低档）
		{120, 40}, // delay 60 → 最高档（取等）
		{500, 40}, // 超最高阈值
	}
	for i, c := range cases {
		s := newSystem(testConfig())
		drive(s, "a", 0, 5, 10, 12, c.deliver)
		r, err := s.Claim("a", c.deliver+5)
		panicIf(err)
		if r.Payout != c.payout {
			t.Fatalf("case %d: want %d got %d", i, c.payout, r.Payout)
		}
	}
	// delay 低于最低阈值：申请成功但无档位赔付，账目记账。
	s := newSystem(testConfig())
	drive(s, "a", 0, 5, 10, 12, 69) // delay 9
	r, err := s.Claim("a", 75)
	panicIf(err)
	if r.Payout != 0 || len(s.Ledger()) != 1 {
		t.Fatalf("below lowest tier: payout=%d ledger=%d", r.Payout, len(s.Ledger()))
	}
}
