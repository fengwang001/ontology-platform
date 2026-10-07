package calendar

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// normErr 把错误归一化为（类别, 子原因, 是否为空）。
func normErr(err error) (ErrCode, Reason, bool) {
	if err == nil {
		return 0, ReasonNone, true
	}
	e, ok := err.(*Error)
	if !ok {
		return -1, ReasonNone, false
	}
	if e == nil {
		return 0, ReasonNone, true
	}
	return e.Code, e.Reason, false
}

func sameErr(a, b error) bool {
	ac, ar, an := normErr(a)
	bc, br, bn := normErr(b)
	return an == bn && (an || (ac == bc && ar == br))
}

func errSummary(err error) string {
	if _, _, isNil := normErr(err); isNil {
		return "OK"
	}
	return err.Error()
}

// diffEnv 同时驱动生产实现 A/B（验证重放确定性）与朴素模型（验证语义）。
type diffEnv struct {
	t     *testing.T
	realA *Service
	realB *Service
	model *naive
	step  int
	now   int64
}

func (e *diffEnv) check3(desc string, e1, e2, e3 error, extra string) {
	e.t.Helper()
	if !sameErr(e1, e2) || !sameErr(e1, e3) {
		e.t.Fatalf("step %d 分叉: %s now=%d realA=%v realB=%v naive=%v",
			e.step, desc, e.now, e1, e2, e3)
	}
	e.t.Logf("step=%d now=%d op=%s => %s %s", e.step, e.now, desc, errSummary(e1), extra)
}

// TestDifferentialRandomOps 用同一随机操作序列驱动生产实现两份与朴素模型一份，
// 逐步对比输出（含退款金额与预订 ID），日志打印每步输入、输出与判定依据。
func TestDifferentialRandomOps(t *testing.T) {
	for _, seed := range []int64{20261006, 1, 7, 99, 12345} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	realA, err := NewService(4, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	realB, _ := NewService(4, 6, 2)
	env := &diffEnv{t: t, realA: realA, realB: realB, model: newNaive(4, 6, 2)}

	listings := []string{"L0", "L1", "L2"}
	prices := []int64{100, 200, 50}
	mins := []int64{1, 2, 3}
	gaps := []int64{0, 1, 2}
	for i, id := range listings {
		e1 := env.realA.AddListing(env.now, id, prices[i], mins[i], gaps[i])
		e2 := env.realB.AddListing(env.now, id, prices[i], mins[i], gaps[i])
		e3 := env.model.addListing(env.now, id, prices[i], mins[i], gaps[i])
		env.check3(fmt.Sprintf("AddListing(%s)", id), e1, e2, e3, "")
	}

	var bookingIDs, blockIDs []int64
	pickBooking := func() int64 {
		if len(bookingIDs) == 0 || rng.Intn(10) == 0 {
			return int64(9999 + rng.Intn(3)) // 不存在的 ID
		}
		if rng.Intn(20) == 0 {
			return 0 // 非法 ID
		}
		return bookingIDs[rng.Intn(len(bookingIDs))]
	}
	pickBlock := func() int64 {
		if len(blockIDs) == 0 || rng.Intn(4) == 0 {
			return int64(9999 + rng.Intn(3))
		}
		return blockIDs[rng.Intn(len(blockIDs))]
	}
	randListing := func() string { return listings[rng.Intn(len(listings))] }

	const steps = 2000
	for env.step = 0; env.step < steps; env.step++ {
		env.now += int64(rng.Intn(3))
		if rng.Intn(20) == 0 {
			env.now -= int64(1 + rng.Intn(3)) // 偶发时钟回退
		}
		now := env.now
		op := rng.Intn(100)
		switch {
		case op < 28: // 创建保留
			l := randListing()
			in := now - 2 + int64(rng.Intn(40))
			out := in + 1 + int64(rng.Intn(6))
			id1, e1 := env.realA.Hold(now, l, in, out)
			id2, e2 := env.realB.Hold(now, l, in, out)
			id3, e3 := env.model.hold(now, l, in, out)
			if e1 == nil && (id1 != id2 || id1 != id3) {
				t.Fatalf("step %d 预订 ID 分叉: Hold(%s,[%d,%d)) now=%d ids=%d,%d,%d errs=%v,%v,%v",
					env.step, l, in, out, now, id1, id2, id3, e1, e2, e3)
			}
			if e1 == nil {
				bookingIDs = append(bookingIDs, id1)
			}
			env.check3(fmt.Sprintf("Hold(%s,[%d,%d))", l, in, out), e1, e2, e3,
				fmt.Sprintf("id=%d", id1))
		case op < 42: // 支付
			id := pickBooking()
			e1 := env.realA.Pay(now, id)
			e2 := env.realB.Pay(now, id)
			e3 := env.model.pay(now, id)
			env.check3(fmt.Sprintf("Pay(%d)", id), e1, e2, e3, "")
		case op < 54: // 取消（对比退款金额）
			id := pickBooking()
			r1, e1 := env.realA.Cancel(now, id)
			r2, e2 := env.realB.Cancel(now, id)
			r3, e3 := env.model.cancel(now, id)
			if e1 == nil && (r1 != r2 || r1 != r3) {
				t.Fatalf("step %d 退款分叉: %d %d %d", env.step, r1, r2, r3)
			}
			env.check3(fmt.Sprintf("Cancel(%d)", id), e1, e2, e3, fmt.Sprintf("refund=%d", r1))
		case op < 64: // 修改日期
			id := pickBooking()
			in := now - 1 + int64(rng.Intn(40))
			out := in + 1 + int64(rng.Intn(6))
			e1 := env.realA.ModifyBooking(now, id, in, out)
			e2 := env.realB.ModifyBooking(now, id, in, out)
			e3 := env.model.modifyBooking(now, id, in, out)
			env.check3(fmt.Sprintf("ModifyBooking(%d,[%d,%d))", id, in, out), e1, e2, e3, "")
		case op < 78: // 查询可订性
			l := randListing()
			in := now - 2 + int64(rng.Intn(40))
			out := in + 1 + int64(rng.Intn(6))
			ok1, e1 := env.realA.CheckAvailability(now, l, in, out)
			ok2, e2 := env.realB.CheckAvailability(now, l, in, out)
			ok3, e3 := env.model.checkAvailability(now, l, in, out)
			if ok1 != ok2 || ok1 != ok3 {
				t.Fatalf("step %d 可订性分叉: %v %v %v", env.step, ok1, ok2, ok3)
			}
			env.check3(fmt.Sprintf("CheckAvailability(%s,[%d,%d))", l, in, out), e1, e2, e3,
				fmt.Sprintf("bookable=%v", ok1))
		case op < 86: // 新增封锁
			l := randListing()
			start := now - 2 + int64(rng.Intn(40))
			end := start + int64(rng.Intn(5))
			id1, e1 := env.realA.AddBlock(now, l, start, end)
			id2, e2 := env.realB.AddBlock(now, l, start, end)
			id3, e3 := env.model.addBlock(now, l, start, end)
			if e1 == nil && (id1 != id2 || id1 != id3) {
				t.Fatalf("step %d 封锁 ID 分叉: %d %d %d", env.step, id1, id2, id3)
			}
			if e1 == nil {
				blockIDs = append(blockIDs, id1)
			}
			env.check3(fmt.Sprintf("AddBlock(%s,[%d,%d))", l, start, end), e1, e2, e3,
				fmt.Sprintf("block=%d", id1))
		case op < 91: // 缩短/延长封锁
			l := randListing()
			blk := pickBlock()
			start := now - 2 + int64(rng.Intn(40))
			end := start + int64(rng.Intn(5))
			e1 := env.realA.ModifyBlock(now, l, blk, start, end)
			e2 := env.realB.ModifyBlock(now, l, blk, start, end)
			e3 := env.model.modifyBlock(now, l, blk, start, end)
			env.check3(fmt.Sprintf("ModifyBlock(%s,%d,[%d,%d))", l, blk, start, end), e1, e2, e3, "")
		case op < 95: // 按日最短入住
			l := randListing()
			day := now - 2 + int64(rng.Intn(40))
			n := 1 + int64(rng.Intn(4))
			e1 := env.realA.SetMinStayForDay(now, l, day, n)
			e2 := env.realB.SetMinStayForDay(now, l, day, n)
			e3 := env.model.setMinStayForDay(now, l, day, n)
			env.check3(fmt.Sprintf("SetMinStayForDay(%s,%d,%d)", l, day, n), e1, e2, e3, "")
		case op < 98: // 换客间隙
			l := randListing()
			g := int64(rng.Intn(3))
			e1 := env.realA.SetGap(now, l, g)
			e2 := env.realB.SetGap(now, l, g)
			e3 := env.model.setGap(now, l, g)
			env.check3(fmt.Sprintf("SetGap(%s,%d)", l, g), e1, e2, e3, "")
		default: // 默认最短入住
			l := randListing()
			n := 1 + int64(rng.Intn(3))
			e1 := env.realA.SetMinStayDefault(now, l, n)
			e2 := env.realB.SetMinStayDefault(now, l, n)
			e3 := env.model.setMinStayDefault(now, l, n)
			env.check3(fmt.Sprintf("SetMinStayDefault(%s,%d)", l, n), e1, e2, e3, "")
		}
	}

	// 终态全面扫描：每个房源未来 45 天的可订性三方一致。
	for d := env.now; d < env.now+45; d++ {
		for _, l := range listings {
			ok1, e1 := env.realA.CheckAvailability(env.now, l, d, d+2)
			ok3, e3 := env.model.checkAvailability(env.now, l, d, d+2)
			if ok1 != ok3 || !sameErr(e1, e3) {
				t.Fatalf("终态扫描分叉: %s [%d,%d) real=%v/%v naive=%v/%v",
					l, d, d+2, ok1, e1, ok3, e3)
			}
		}
	}
	t.Logf("终态扫描一致：%d 个房源 x 45 天，共 %d 步操作", len(listings), steps)
}

// TestConcurrentHoldsNoDoubleBooking 并发创建保留不得使同一夜被两笔保留占用，
// 且整体结果等价于某个串行顺序（成功集合互不相交）。
func TestConcurrentHoldsNoDoubleBooking(t *testing.T) {
	s, err := NewService(5, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddListing(0, "L", 100, 1, 0); err != nil {
		t.Fatal(err)
	}
	// 同一区间并发抢保留：恰好一笔成功
	const g = 64
	var wg sync.WaitGroup
	errs := make([]error, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Hold(10, "L", 20, 25)
		}(i)
	}
	wg.Wait()
	success := 0
	for _, e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("同一区间并发保留成功数 = %d，应为 1", success)
	}
	// 混合并发：不相交区间保留 + 支付 + 取消 + 查询
	const g2 = 32
	ids := make([]int64, g2)
	errs2 := make([]error, g2)
	for i := 0; i < g2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := int64(100 + 10*i)
			ids[i], errs2[i] = s.Hold(10, "L", in, in+5)
			if errs2[i] == nil && i%2 == 0 {
				_ = s.Pay(10, ids[i])
			}
			if errs2[i] == nil && i%4 == 0 {
				_, _ = s.Cancel(10, ids[i])
			}
			_, _ = s.CheckAvailability(10, "L", in, in+5)
		}(i)
	}
	wg.Wait()
	for i, e := range errs2 {
		if e != nil {
			t.Fatalf("不相交区间 %d 应成功: %v", i, e)
		}
	}
	// 不变量：任意两笔活跃预订的夜集合互不相交
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := map[int64]int64{}
	for _, b := range s.bookings {
		if !b.activeAt(s.lastNow) {
			continue
		}
		for n := b.CheckIn; n < b.CheckOut; n++ {
			if prev, dup := owner[n]; dup {
				t.Fatalf("夜 %d 被预订 %d 与 %d 同时占用", n, prev, b.ID)
			}
			owner[n] = b.ID
		}
	}
}

// TestAvailabilityCostIndependentOfHistory 复杂度证明：
// 可订性判定的占用探测次数不随历史预订总数增长。
func TestAvailabilityCostIndependentOfHistory(t *testing.T) {
	build := func(numBookings int) (*Service, *Listing) {
		s, err := NewService(5, 7, 3)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AddListing(0, "L", 100, 2, 1); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < numBookings; i++ {
			in := int64(1000 + 10*i)
			id, err := s.Hold(0, "L", in, in+2)
			if err != nil {
				t.Fatalf("Hold %d: %v", i, err)
			}
			if err := s.Pay(0, id); err != nil {
				t.Fatalf("Pay %d: %v", i, err)
			}
		}
		return s, s.listings["L"]
	}
	probe := func(s *Service, l *Listing) int64 {
		l.probes = 0
		ok, err := s.CheckAvailability(0, "L", 500, 503) // 距所有预订很远
		if !ok {
			t.Fatalf("应可订: %v", err)
		}
		return l.probes
	}
	s1, l1 := build(2)
	s2, l2 := build(4000)
	p1 := probe(s1, l1)
	p2 := probe(s2, l2)
	// 上界 = 区间夜数 + 两侧各 (Gap+maxMinStay) 次邻居探测
	bound := int64(3 + 2*(1+2))
	if p1 != p2 {
		t.Fatalf("探测次数随历史增长: 2 笔=%d, 4000 笔=%d", p1, p2)
	}
	if p2 > bound {
		t.Fatalf("探测次数 %d 超过理论上界 %d", p2, bound)
	}
	t.Logf("探测次数: 2 笔历史=%d, 4000 笔历史=%d, 上界=%d", p1, p2, bound)
}
