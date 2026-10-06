package kitchen

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustKitchen(t *testing.T, cfg Config) *Kitchen {
	t.Helper()
	k, err := NewKitchen(cfg)
	if err != nil {
		t.Fatalf("NewKitchen: %v", err)
	}
	return k
}

func mustAccept(t *testing.T, k *Kitchen, now int64, id string, dur int64) AcceptResult {
	t.Helper()
	res, err := k.AcceptInstant(now, id, dur)
	if err != nil {
		t.Fatalf("AcceptInstant(%d, %s, %d): %v", now, id, dur, err)
	}
	return res
}

func assertEvents(t *testing.T, k *Kitchen, want []PressureEvent) {
	t.Helper()
	got := k.PressureEvents()
	if len(got) != len(want) {
		t.Fatalf("压单事件数 = %d (%v)，期望 %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("压单事件[%d] = %+v，期望 %+v（全部：%v）", i, got[i], want[i], got)
		}
	}
}

// 预计等待恰等于进入阈值则压单接单；恰等于爆单阈值则拒单；
// 恰等于退出阈值不退出（严格小于才退出）；介于两阈值之间保持原状态。
func TestThresholdBoundariesAndHysteresis(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 20})

	res := mustAccept(t, k, 0, "A", 100) // 立即开工，estEnd=100
	if res.Pressured || res.PromisedPickup != 110 {
		t.Fatalf("正常单承诺取货 = %d，期望 110", res.PromisedPickup)
	}

	// 预计等待恰等于爆单阈值 20：拒单，且不改压单状态、队列与时钟。
	if _, err := k.AcceptInstant(80, "D", 1); !errors.Is(err, ErrBurst) {
		t.Fatalf("wait=20 应报爆单，得到 %v", err)
	}
	if _, ok := k.Order("D"); ok {
		t.Fatal("爆单拒单不得留下订单")
	}
	assertEvents(t, k, nil)

	// 预计等待恰等于进入阈值 10：压单接单，承诺取货按推定完成时刻。
	res = mustAccept(t, k, 90, "B", 5)
	if !res.Pressured || res.EstimatedWait != 10 || res.PromisedPickup != 105 {
		t.Fatalf("压单单结果 = %+v，期望 wait=10 promise=105", res)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	// 介于进入阈值与爆单阈值之间：仍为压单单，但不重复产生进入事件。
	res = mustAccept(t, k, 91, "C", 1) // B@100，C 推定 105，wait=14
	if !res.Pressured || res.EstimatedWait != 14 {
		t.Fatalf("C 结果 = %+v，期望 wait=14 压单", res)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	// 取消 B 后重新推定 wait=5，恰等于退出阈值：严格小于才退出，保持压单。
	if err := k.Cancel(96, "B"); err != nil {
		t.Fatalf("Cancel B: %v", err)
	}
	if !k.Pressured() {
		t.Fatal("wait=5 恰等于退出阈值，不应退出压单")
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	// 再取消 C 后 wait=4 < 退出阈值：退出压单。
	if err := k.Cancel(96, "C"); err != nil {
		t.Fatalf("Cancel C: %v", err)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}, {At: 96, Enter: false}})
}

// 目标开工时刻恰等于本单推定开工时刻的预约单必须计入推定。
func TestReservationCountedAtExactEstimatedStart(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 100, ExitThreshold: 50, BurstThreshold: 10000})

	mustAccept(t, k, 0, "A", 50)                                   // A 开工于 0，estEnd=50
	if _, err := k.AcceptReservation(0, "R", 30, 80); err != nil { // 目标开工时刻=50
		t.Fatalf("AcceptReservation: %v", err)
	}
	// B 的推定开工时刻本为 50，恰等于 R 的目标开工时刻，R 必须计入：
	// R@50（estEnd=80），B 推定开工 80，wait=80。
	res := mustAccept(t, k, 0, "B", 10)
	if res.EstimatedWait != 80 {
		t.Fatalf("计入目标开工时刻恰等于推定开工时刻的预约单后 wait 应为 80，得到 %d", res.EstimatedWait)
	}

	if err := k.Complete(50, "A"); err != nil {
		t.Fatalf("Complete A: %v", err)
	}
	info, _ := k.Order("R")
	if info.State != "cooking" || info.StartTime != 50 {
		t.Fatalf("R 应于 50 开工，得到 %+v", info)
	}
	if err := k.Complete(80, "R"); err != nil {
		t.Fatalf("Complete R: %v", err)
	}
	info, _ = k.Order("B")
	if info.State != "cooking" || info.StartTime != 80 {
		t.Fatalf("B 应于 80 开工，得到 %+v", info)
	}
}

// 预约单到达目标开工时刻后，优先于所有尚未开工的即时单开工。
func TestReservationCutsInAfterTargetStart(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 100, ExitThreshold: 50, BurstThreshold: 10000})

	mustAccept(t, k, 0, "A", 100)                                   // A 开工于 0，estEnd=100
	if _, err := k.AcceptReservation(0, "R", 20, 120); err != nil { // 目标开工时刻=100
		t.Fatalf("AcceptReservation: %v", err)
	}
	res := mustAccept(t, k, 0, "B", 5) // B 先排队，但 R 到达后应插队
	if res.EstimatedWait != 120 {
		t.Fatalf("R 插队在即，B 的 wait 应为 120，得到 %d", res.EstimatedWait)
	}

	if err := k.Complete(100, "A"); err != nil {
		t.Fatalf("Complete A: %v", err)
	}
	info, _ := k.Order("R")
	if info.State != "cooking" || info.StartTime != 100 {
		t.Fatalf("R 应于目标开工时刻 100 开工，得到 %+v", info)
	}
	info, _ = k.Order("B")
	if info.State != "waiting" {
		t.Fatalf("B 应仍在排队，得到 %+v", info)
	}
	if err := k.Complete(120, "R"); err != nil {
		t.Fatalf("Complete R: %v", err)
	}
	info, _ = k.Order("B")
	if info.State != "cooking" || info.StartTime != 120 {
		t.Fatalf("B 应于 120 开工，得到 %+v", info)
	}
}

// 提前完成立即释放制作位，并触发重新推定从而退出压单。
func TestEarlyCompletionTriggersExit(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 1000})

	mustAccept(t, k, 0, "A", 100) // estEnd=100
	mustAccept(t, k, 90, "B", 7)  // wait=10，进入压单
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	if err := k.Complete(96, "A"); err != nil { // 提前 4 秒完成
		t.Fatalf("Complete A: %v", err)
	}
	info, _ := k.Order("B")
	if info.State != "cooking" || info.StartTime != 96 {
		t.Fatalf("提前完成应立即释放制作位，B 应于 96 开工，得到 %+v", info)
	}
	// B 开工后假想单排在 B 后，wait=7 介于两阈值之间，保持压单。
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	if err := k.Complete(100, "B"); err != nil { // 提前 3 秒完成，队列清空
		t.Fatalf("Complete B: %v", err)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}, {At: 100, Enter: false}})
}

// 暂停期间新单一律报商家暂停，且拒单不改变压单状态记录；
// 排队中与制作中的订单照常推进，恢复后从当时队列重新推定。
func TestPauseRejectionsKeepPressureState(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 1000})

	mustAccept(t, k, 0, "A", 100)
	mustAccept(t, k, 90, "B", 7) // 进入压单
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	if err := k.Pause(91); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if _, err := k.AcceptInstant(92, "C", 1); !errors.Is(err, ErrMerchantPaused) {
		t.Fatalf("暂停期间即时单应报商家暂停，得到 %v", err)
	}
	if _, err := k.AcceptReservation(92, "R", 5, 1000); !errors.Is(err, ErrMerchantPaused) {
		t.Fatalf("暂停期间预约单应报商家暂停，得到 %v", err)
	}
	if _, ok := k.Order("C"); ok {
		t.Fatal("暂停拒单不得留下订单")
	}
	if !k.Pressured() {
		t.Fatal("暂停期间的拒单不得改变压单状态")
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	// 暂停期间制作中的订单照常推进：完成报告受理，B 开工。
	if err := k.Complete(93, "A"); err != nil {
		t.Fatalf("暂停期间完成报告应受理: %v", err)
	}
	info, _ := k.Order("B")
	if info.State != "cooking" || info.StartTime != 93 {
		t.Fatalf("暂停期间 B 应照常开工于 93，得到 %+v", info)
	}
	// B estEnd=100，假想单 wait=7 介于两阈值之间，压单保持。
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})

	// 暂停期间提前完成 B，队列清空，触发退出压单（暂停与压单互不影响）。
	if err := k.Complete(97, "B"); err != nil {
		t.Fatalf("暂停期间完成报告应受理: %v", err)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}, {At: 97, Enter: false}})

	if err := k.Resume(98); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	res := mustAccept(t, k, 99, "C", 1) // 制作位空闲，wait=0，正常接单
	if res.Pressured || res.EstimatedWait != 0 || res.PromisedPickup != 110 {
		t.Fatalf("恢复后接单结果 = %+v，期望 wait=0 promise=110 非压单", res)
	}
}

// 取消排队单立即释放队列位置，重新推定的预计等待下降并可触发退出压单。
func TestCancelQueuedLowersWait(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 1000})

	mustAccept(t, k, 0, "A", 100)
	mustAccept(t, k, 90, "B", 6) // wait=10，进入压单
	res := mustAccept(t, k, 91, "C", 6)
	if res.EstimatedWait != 15 {
		t.Fatalf("取消前 C 的 wait 应为 15，得到 %d", res.EstimatedWait)
	}

	if err := k.Cancel(92, "B"); err != nil {
		t.Fatalf("Cancel B: %v", err)
	}
	// B 取消后假想单 wait 降到 14，仍高于退出阈值，保持压单。
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}})
	if got := k.PreviewWait(92, 1); got != 14 {
		t.Fatalf("取消 B 后预计等待应降为 14，得到 %d", got)
	}

	if err := k.Cancel(96, "C"); err != nil {
		t.Fatalf("Cancel C: %v", err)
	}
	// C 取消后队列清空，wait=4 < 退出阈值，退出压单。
	if got := k.PreviewWait(96, 1); got != 4 {
		t.Fatalf("取消 C 后预计等待应降为 4，得到 %d", got)
	}
	assertEvents(t, k, []PressureEvent{{At: 90, Enter: true}, {At: 96, Enter: false}})

	// 状态类错误：取消已开工、已完成、不存在的订单。
	if err := k.Cancel(96, "A"); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("取消已开工订单应报已开工，得到 %v", err)
	}
	if err := k.Cancel(96, "B"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("取消已取消订单应报不存在，得到 %v", err)
	}
}

// 压单进入与退出事件严格交替。
func TestPressureEventsAlternate(t *testing.T) {
	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 1000})

	for cycle := int64(0); cycle < 3; cycle++ {
		base := cycle * 200
		x := fmt.Sprintf("X%d", cycle)
		y := fmt.Sprintf("Y%d", cycle)
		mustAccept(t, k, base, x, 100)
		mustAccept(t, k, base+90, y, 5) // wait=10，进入压单
		if err := k.Complete(base+91, x); err != nil {
			t.Fatalf("Complete %s: %v", x, err)
		}
		if err := k.Complete(base+92, y); err != nil {
			t.Fatalf("Complete %s: %v", y, err)
		}
	}

	events := k.PressureEvents()
	if len(events) != 6 {
		t.Fatalf("应产生 6 个压单事件，得到 %v", events)
	}
	for i, e := range events {
		wantEnter := i%2 == 0
		if e.Enter != wantEnter {
			t.Fatalf("压单事件未严格交替：%v", events)
		}
	}
}

// 拒绝次序：参数非法、时钟回退、订单不存在或已存在、状态类错误、
// 商家暂停、预约过近、爆单；每个操作只报第一个命中的错误。
func TestErrorPrecedenceAndClock(t *testing.T) {
	bad := []Config{
		{ParallelLimit: 0, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 20},
		{ParallelLimit: 1, EnterThreshold: 5, ExitThreshold: 5, BurstThreshold: 20},
		{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 10},
		{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: -1, BurstThreshold: 20},
		{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 20, ReservationLead: -1},
	}
	for i, cfg := range bad {
		if _, err := NewKitchen(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("非法构造参数[%d]应报参数非法，得到 %v", i, err)
		}
	}

	k := mustKitchen(t, Config{ParallelLimit: 1, EnterThreshold: 10, ExitThreshold: 5, BurstThreshold: 20, ReservationLead: 5})

	if _, err := k.AcceptInstant(-1, "x", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("负时刻应报参数非法，得到 %v", err)
	}
	if _, err := k.AcceptInstant(0, "x", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("非正时长应报参数非法，得到 %v", err)
	}
	mustAccept(t, k, 10, "A", 5)
	if _, err := k.AcceptInstant(9, "B", 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("时刻回退应报时钟回退，得到 %v", err)
	}
	// 被拒绝的操作不推进时钟：t=10 仍被接受。
	mustAccept(t, k, 10, "B", 5)
	if _, err := k.AcceptInstant(10, "A", 5); !errors.Is(err, ErrOrderExists) {
		t.Fatalf("重复订单号应报已存在，得到 %v", err)
	}

	// 暂停先于预约过近判定。
	if err := k.Pause(10); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if _, err := k.AcceptReservation(10, "R2", 5, 12); !errors.Is(err, ErrMerchantPaused) {
		t.Fatalf("暂停应先于预约过近命中，得到 %v", err)
	}
	if err := k.Resume(10); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := k.AcceptReservation(10, "R2", 5, 12); !errors.Is(err, ErrReservationTooSoon) {
		t.Fatalf("目标开工时刻 7 < 10+5 应报预约过近，得到 %v", err)
	}
	if _, err := k.AcceptReservation(10, "R3", 5, 20); err != nil { // 目标开工时刻 15 = 10+5，恰好允许
		t.Fatalf("目标开工时刻恰等于 当前+提前量 应被接受，得到 %v", err)
	}

	if err := k.Complete(10, "nope"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("完成不存在订单应报不存在，得到 %v", err)
	}
	if err := k.Complete(10, "B"); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("完成未开工订单应报未开工，得到 %v", err)
	}
	if err := k.Complete(10, "A"); err != nil {
		t.Fatalf("Complete A: %v", err)
	}
	if err := k.Complete(10, "A"); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("重复完成应报已完成，得到 %v", err)
	}
	if err := k.Cancel(10, "A"); !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("取消已完成订单应报已完成，得到 %v", err)
	}
	// A 完成后 B 已开工（R3 目标开工时刻 15 未到），取消应报已开工。
	if err := k.Cancel(10, "B"); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("取消已开工订单应报已开工，得到 %v", err)
	}
	mustAccept(t, k, 10, "E", 1) // 排队中
	if err := k.Cancel(10, "E"); err != nil {
		t.Fatalf("取消排队单应成功: %v", err)
	}
}

// 并发接单不突破制作上限，重复订单号恰好被接受一次。
func TestConcurrentAdmissionRespectsLimit(t *testing.T) {
	const limit = 4
	const total = 200
	k := mustKitchen(t, Config{ParallelLimit: limit, EnterThreshold: 1 << 40, ExitThreshold: 0, BurstThreshold: 1 << 50})

	var wg sync.WaitGroup
	errs := make([]error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = k.AcceptInstant(0, fmt.Sprintf("o%d", i), 5)
		}(i)
	}
	// 同一订单号并发提交两次：恰好一次成功。
	dupErrs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, dupErrs[i] = k.AcceptInstant(0, "dup", 5)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发接单 o%d 失败: %v", i, err)
		}
	}
	dupOK := 0
	for _, err := range dupErrs {
		if err == nil {
			dupOK++
		} else if !errors.Is(err, ErrOrderExists) {
			t.Fatalf("重复订单号应报已存在，得到 %v", err)
		}
	}
	if dupOK != 1 {
		t.Fatalf("同一订单号应恰好被接受一次，实际 %d 次", dupOK)
	}
	if got := k.CookingCount(); got != limit {
		t.Fatalf("制作中订单数 = %d，应等于并行上限 %d", got, limit)
	}

	// 并发完成当前制作中的订单：空出的制作位立即被后续订单占用，仍不超上限。
	var cooking []string
	for i := 0; i < total; i++ {
		if info, _ := k.Order(fmt.Sprintf("o%d", i)); info.State == "cooking" {
			cooking = append(cooking, fmt.Sprintf("o%d", i))
		}
	}
	if info, _ := k.Order("dup"); info.State == "cooking" {
		cooking = append(cooking, "dup")
	}
	if len(cooking) != limit {
		t.Fatalf("制作中订单数 = %d，期望 %d", len(cooking), limit)
	}
	for _, id := range cooking {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := k.Complete(1, id); err != nil {
				t.Errorf("Complete %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	if got := k.CookingCount(); got > limit {
		t.Fatalf("制作中订单数 %d 突破并行上限 %d", got, limit)
	}
}

// 一次准入判定与完成报告引发的重新推定，开销只与当前排队及制作中的
// 订单数有关，不随历史已完成订单数增长。用推定调度计数器确定性验证。
func TestEstimationCostIndependentOfHistory(t *testing.T) {
	cfg := Config{ParallelLimit: 2, EnterThreshold: 1 << 30, ExitThreshold: 0, BurstThreshold: 1 << 40}

	build := func(withHistory bool) *Kitchen {
		k := mustKitchen(t, cfg)
		if withHistory {
			for i := 0; i < 2000; i++ { // 制造 2000 笔已完成订单的历史
				id := fmt.Sprintf("h%d", i)
				tm := int64(i)
				if _, err := k.AcceptInstant(tm, id, 1); err != nil {
					t.Fatalf("构造历史接单失败: %v", err)
				}
				if err := k.Complete(tm, id); err != nil {
					t.Fatalf("构造历史完成失败: %v", err)
				}
			}
		}
		// 当前状态：2 笔制作中 + 3 笔排队，两厨房完全一致。
		for _, id := range []string{"c0", "c1", "q0", "q1", "q2"} {
			if _, err := k.AcceptInstant(5000, id, 100); err != nil {
				t.Fatalf("构造当前状态失败: %v", err)
			}
		}
		return k
	}

	fresh := build(false)
	historied := build(true)

	// 准入判定：等待订单 = 3 笔排队 + 1 笔假想单 = 4 次调度。
	for name, k := range map[string]*Kitchen{"fresh": fresh, "historied": historied} {
		before := k.EstimateOps()
		if _, err := k.AcceptInstant(5000, "probe", 5); err != nil {
			t.Fatalf("%s 准入失败: %v", name, err)
		}
		if got := k.EstimateOps() - before; got != 4 {
			t.Fatalf("%s 准入判定调度 %d 单，期望 4（与历史无关）", name, got)
		}
	}
	// 完成报告引发的重新推定：队首开工后剩 3 笔排队 + 1 笔假想单 = 4 次调度。
	for name, k := range map[string]*Kitchen{"fresh": fresh, "historied": historied} {
		before := k.EstimateOps()
		if err := k.Complete(5000, "c0"); err != nil {
			t.Fatalf("%s Complete: %v", name, err)
		}
		if got := k.EstimateOps() - before; got != 4 {
			t.Fatalf("%s 完成报告重新推定调度 %d 单，期望 4（与历史无关）", name, got)
		}
	}
}

// 基准：大量历史已完成订单后，准入判定（爆单拒单路径，不改状态）的开销保持恒定。
func BenchmarkBurstAdmissionAfterHistory(b *testing.B) {
	k, _ := NewKitchen(Config{ParallelLimit: 1, EnterThreshold: 1, ExitThreshold: 0, BurstThreshold: 1 << 40})
	for i := 0; i < 10000; i++ {
		tm := int64(2 * i)
		id := fmt.Sprintf("h%d", i)
		k.AcceptInstant(tm, id, 1)
		k.Complete(tm, id)
	}
	k.AcceptInstant(20000, "c", 1<<30)
	for i := 0; i < 8; i++ {
		k.AcceptInstant(20000, fmt.Sprintf("q%d", i), 100)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k.PreviewWait(20000, 5)
	}
}
