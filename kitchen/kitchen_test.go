package kitchen

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testCfg() Config {
	return Config{Parallelism: 1, EnterThreshold: 10, ExitThreshold: 3, RejectThreshold: 30, ReservationLead: 5}
}

func mustAdmit(t *testing.T, k *Kitchen, req OrderRequest, now int64) AdmitResult {
	t.Helper()
	res, err := k.Admit(req, now)
	if err != nil {
		t.Fatalf("admit %s at %d: unexpected error: %v", req.ID, now, err)
	}
	return res
}

func codeIs(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var ke *Error
	if !errors.As(err, &ke) {
		t.Fatalf("want kitchen error, got %v", err)
	}
	if ke.Code != want {
		t.Fatalf("want code %d, got %d (%v)", want, ke.Code, err)
	}
}

// 预计等待恰等于进入阈值：接单且压单，承诺按推定完成时刻。
func TestWaitExactlyEnterThreshold(t *testing.T) {
	k, err := New(testCfg())
	if err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 10}, 0)
	res := mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 7}, 0)
	if res.ExpectedWait != 10 {
		t.Fatalf("want wait 10, got %d", res.ExpectedWait)
	}
	if !res.Pressed {
		t.Fatalf("want pressed at exact enter threshold")
	}
	if res.PromisePickup != 17 {
		t.Fatalf("promise = estimated finish 17, got %d", res.PromisePickup)
	}
	ev := k.PressureEvents()
	if len(ev) != 1 || !ev[0].Entered {
		t.Fatalf("want single enter event, got %+v", ev)
	}
}

// 预计等待恰等于爆单阈值：拒单报爆单，不改时钟、队列与压单。
func TestWaitExactlyRejectThreshold(t *testing.T) {
	k, _ := New(testCfg())
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 30}, 0)
	_, err := k.Admit(OrderRequest{ID: "b", Kind: Immediate, Duration: 1}, 0)
	codeIs(t, err, ErrOverloaded)
	if k.Now() != 0 {
		t.Fatalf("rejected op must not advance clock, got %d", k.Now())
	}
	if k.Pressed() {
		t.Fatalf("rejected overloaded op must not enter pressure")
	}
	if _, ok := k.Order("b"); ok {
		t.Fatalf("rejected order must not be stored")
	}
}

// 滞回：等待处于 [退出阈值, 进入阈值) 中间区间时保持原压单状态。
func TestPressureHysteresis(t *testing.T) {
	cfg := testCfg() // enter 10, exit 3
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 20}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 5}, 0) // 等待 20 => 进入
	mustAdmit(t, k, OrderRequest{ID: "c", Kind: Immediate, Duration: 5}, 0)
	if !k.Pressed() {
		t.Fatal("want pressed")
	}
	// t=15 完成 a：b 开工到 20；c 等待 5，落在 [3,10)，必须保持压单。
	if err := k.Complete("a", 15); err != nil {
		t.Fatal(err)
	}
	if !k.Pressed() {
		t.Fatal("wait 5 is in hysteresis band: pressure must remain")
	}
	if ev := k.PressureEvents(); len(ev) != 1 {
		t.Fatalf("want only the enter event, got %+v", ev)
	}
	// t=20 时 b 已到推定完成时刻并被 c 接续；依次完成 b、c，队列清空 => 退出。
	if err := k.Complete("b", 20); err != nil {
		t.Fatal(err)
	}
	if err := k.Complete("c", 25); err != nil {
		t.Fatal(err)
	}
	if k.Pressed() {
		t.Fatal("empty queue must exit pressure after both finish")
	}
	events := k.PressureEvents()
	if len(events) != 2 || events[1].Entered {
		t.Fatalf("want enter then exit, got %+v", events)
	}
}

// 目标开工时刻恰等于本单推定开工时刻的预约单计入且插队优先。
func TestReservationTargetEqualsEstimatedStart(t *testing.T) {
	k, _ := New(testCfg())
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 10}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 5}, 0)
	mustAdmit(t, k, OrderRequest{ID: "r", Kind: Reservation, Duration: 5, TargetPickup: 15}, 0)
	if err := k.Tick(10); err != nil {
		t.Fatal(err)
	}
	rInfo, _ := k.Order("r")
	bInfo, _ := k.Order("b")
	if rInfo.Status != StatusCooking || rInfo.StartAt != 10 {
		t.Fatalf("reservation should start at 10, got status=%d start=%d", rInfo.Status, rInfo.StartAt)
	}
	if bInfo.Status != StatusWaiting {
		t.Fatalf("immediate b must wait behind reservation, got %d", bInfo.Status)
	}
}

// 预约单到达目标开工时刻后插队；晚于即时单推定开工时刻的预约单不计入。
func TestReservationArrivalJumpAndExclusion(t *testing.T) {
	cfg := Config{Parallelism: 1, EnterThreshold: 100, ExitThreshold: 1, RejectThreshold: 200, ReservationLead: 0}
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 4}, 0)
	mustAdmit(t, k, OrderRequest{ID: "i1", Kind: Immediate, Duration: 2}, 0)
	mustAdmit(t, k, OrderRequest{ID: "far", Kind: Reservation, Duration: 2, TargetPickup: 102}, 0)
	i1Info, _ := k.Order("i1")
	if i1Info.EstimatedStart != 4 {
		t.Fatalf("far reservation must not count, i1 estimated start want 4 got %d", i1Info.EstimatedStart)
	}
	mustAdmit(t, k, OrderRequest{ID: "near", Kind: Reservation, Duration: 2, TargetPickup: 6}, 0)
	if err := k.Tick(4); err != nil {
		t.Fatal(err)
	}
	near, _ := k.Order("near")
	if near.Status != StatusCooking || near.StartAt != 4 {
		t.Fatalf("near reservation jumps at target, got %+v", near)
	}
	i1, _ := k.Order("i1")
	if i1.Status != StatusWaiting {
		t.Fatal("i1 must still wait behind near reservation")
	}
}

// 提前完成立即释放制作位并触发压单退出。
func TestEarlyCompletionTriggersExit(t *testing.T) {
	cfg := testCfg()
	cfg.RejectThreshold = 1000
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 100}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 1}, 0)
	if !k.Pressed() {
		t.Fatal("want pressed")
	}
	if err := k.Complete("a", 1); err != nil {
		t.Fatal(err)
	}
	if k.Pressed() {
		t.Fatal("early completion must release slot and exit pressure")
	}
	bInfo, _ := k.Order("b")
	if bInfo.StartAt != 1 {
		t.Fatalf("b should start at 1, got %d", bInfo.StartAt)
	}
}

// 暂停期间新单报商家暂停且不改压单；在制订单照常推进。
func TestPauseRejectsWithoutTouchingPressure(t *testing.T) {
	cfg := testCfg()
	cfg.RejectThreshold = 1000
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 20}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 1}, 0)
	if err := k.Pause(5); err != nil {
		t.Fatal(err)
	}
	_, err := k.Admit(OrderRequest{ID: "c", Kind: Immediate, Duration: 1}, 6)
	codeIs(t, err, ErrMerchantPaused)
	_, err = k.Admit(OrderRequest{ID: "r", Kind: Reservation, Duration: 1, TargetPickup: 100}, 6)
	codeIs(t, err, ErrMerchantPaused)
	if !k.Pressed() {
		t.Fatal("pause rejects must not change pressure state")
	}
	if err := k.Complete("a", 7); err != nil {
		t.Fatal(err)
	}
	if k.Pressed() {
		t.Fatal("completion released slot: pressure should exit even while paused")
	}
	if err := k.Resume(8); err != nil {
		t.Fatal(err)
	}
	if k.Paused() {
		t.Fatal("resume should clear paused flag")
	}
}

// 取消排队单后预计等待下降；已开工取消、未开工完成分别报状态类错误。
func TestCancelLowersWaitAndStateErrors(t *testing.T) {
	k, _ := New(testCfg())
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 10}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 10}, 0)
	c := mustAdmit(t, k, OrderRequest{ID: "c", Kind: Immediate, Duration: 10}, 0)
	if c.ExpectedWait != 20 {
		t.Fatalf("want wait 20, got %d", c.ExpectedWait)
	}
	if err := k.Cancel("b", 0); err != nil {
		t.Fatal(err)
	}
	// 取消 b 后队列只剩 c（a->10, c->20）：下一笔假想单等待 20，
	// 相对取消前（a->10, b->20, c->30，假想等待 30）下降 10。
	if w := hypotheticalTestWait(k, 0); w != 20 {
		t.Fatalf("want wait 20 after cancel, got %d", w)
	}
	// 再取消 c：队列清空，假想等待降为 10（仅剩在制 a 到 t=10），处于
	// 中间区间 [3,10)，保持压单，不产生退出事件。
	if err := k.Cancel("c", 0); err != nil {
		t.Fatal(err)
	}
	if w := hypotheticalTestWait(k, 0); w != 10 {
		t.Fatalf("want wait 10 after cancelling all queued, got %d", w)
	}
	if !k.Pressed() {
		t.Fatal("wait 10 sits in hysteresis band: must remain pressed")
	}
	if err := k.Tick(10); err != nil {
		t.Fatal(err)
	}
	codeIs(t, k.Cancel("a", 10), ErrAlreadyStarted)
	codeIs(t, k.Complete("b", 10), ErrNotStarted)
	// t=10 完成 a，队列空、等待 0 < 退出阈值 => 退出压单。
	if err := k.Complete("a", 10); err != nil {
		t.Fatal(err)
	}
	if k.Pressed() {
		t.Fatal("completing a empties the queue: must exit pressure")
	}
}

func hypotheticalTestWait(k *Kitchen, now int64) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.hypotheticalWait(now)
}

// 进入与退出事件严格交替且序号单调。
func TestEventsStrictlyAlternate(t *testing.T) {
	cfg := testCfg()
	cfg.EnterThreshold = 10
	cfg.ExitThreshold = 3
	cfg.RejectThreshold = 1000
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 20}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 20}, 0)
	if err := k.Complete("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := k.Complete("b", 1); err != nil {
		t.Fatal(err)
	}
	mustAdmit(t, k, OrderRequest{ID: "c", Kind: Immediate, Duration: 50}, 2)
	mustAdmit(t, k, OrderRequest{ID: "d", Kind: Immediate, Duration: 1}, 2)
	if err := k.Complete("c", 3); err != nil {
		t.Fatal(err)
	}
	if err := k.Complete("d", 3); err != nil {
		t.Fatal(err)
	}
	events := k.PressureEvents()
	if len(events) != 4 {
		t.Fatalf("want 4 alternating events, got %d: %+v", len(events), events)
	}
	for i, ev := range events {
		if ev.Entered != (i%2 == 0) || ev.Seq != i+1 {
			t.Fatalf("event %d not alternating/ordered: %+v", i, ev)
		}
	}
}

// 拒绝次序与时钟回退等错误码可程序化区分。
func TestErrorOrderingAndClock(t *testing.T) {
	k, _ := New(testCfg())
	_, err := k.Admit(OrderRequest{ID: "", Kind: Immediate, Duration: 1}, 0)
	codeIs(t, err, ErrInvalidParam)
	_, err = k.Admit(OrderRequest{ID: "a", Kind: Immediate, Duration: 0}, 0)
	codeIs(t, err, ErrInvalidParam)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 1}, 5)
	_, err = k.Admit(OrderRequest{ID: "a", Kind: Immediate, Duration: 1}, 5)
	codeIs(t, err, ErrOrderExists)
	_, err = k.Admit(OrderRequest{ID: "z", Kind: Immediate, Duration: 1}, 4)
	codeIs(t, err, ErrClockBackward)
	codeIs(t, k.Complete("nope", 6), ErrOrderNotFound)
	// 预约过近：lead 5，now 5 => 目标开工最早 10。
	_, err = k.Admit(OrderRequest{ID: "r", Kind: Reservation, Duration: 3, TargetPickup: 12}, 5)
	codeIs(t, err, ErrReservationTooSoon)
	// 非法构造参数。
	if _, err := New(Config{Parallelism: 0}); err == nil {
		t.Fatal("want invalid config error")
	}
}

func id(i int) string { return fmt.Sprintf("o%03d", i) }

// 并发接单不突破制作上限；任意串行化下在制数恒不超过并行数。
func TestConcurrentAdmitRespectsParallelism(t *testing.T) {
	cfg := Config{Parallelism: 3, EnterThreshold: 1, ExitThreshold: 0, RejectThreshold: 1_000_000, ReservationLead: 0}
	k, _ := New(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = k.Admit(OrderRequest{ID: id(i), Kind: Immediate, Duration: 100}, 0)
		}(i)
	}
	wg.Wait()
	cooking, waiting := k.Counters()
	if cooking > cfg.Parallelism {
		t.Fatalf("cooking %d exceeds parallelism %d", cooking, cfg.Parallelism)
	}
	accepted := 0
	for i := 0; i < 60; i++ {
		if _, ok := k.Order(id(i)); ok {
			accepted++
		}
	}
	if accepted != 60 {
		t.Fatalf("all 60 orders must be accepted, got %d", accepted)
	}
	if cooking+waiting != 60 {
		t.Fatalf("cooking+waiting want 60, got %d (c=%d w=%d)", cooking+waiting, cooking, waiting)
	}
	for _, info := range k.CookingOrders() {
		if info.StartAt != 0 {
			t.Fatalf("cooking order must start at 0, got %+v", info)
		}
	}
}

// 预约单准入只看提前量，不受压单与爆单影响；承诺取货即目标取货。
func TestReservationAdmissionUnaffectedByPressure(t *testing.T) {
	cfg := testCfg()
	cfg.RejectThreshold = 1000
	k, _ := New(cfg)
	mustAdmit(t, k, OrderRequest{ID: "a", Kind: Immediate, Duration: 100}, 0)
	mustAdmit(t, k, OrderRequest{ID: "b", Kind: Immediate, Duration: 1}, 0) // 已压单
	res, err := k.Admit(OrderRequest{ID: "r", Kind: Reservation, Duration: 2, TargetPickup: 8}, 1)
	if err != nil {
		t.Fatalf("reservation must ignore pressure/overload: %v", err)
	}
	if !res.Accepted || res.PromisePickup != 8 {
		t.Fatalf("reservation accepted with promise=target pickup, got %+v", res)
	}
}

// BenchmarkAdmitScalesWithLiveOrders 验证一次即时单准入判定只与当前
// 在制/在队订单数相关：制造大量“已完成”历史后，准入耗时不随历史增长。
func BenchmarkAdmitScalesWithLiveOrders(b *testing.B) {
	cfg := Config{Parallelism: 4, EnterThreshold: 1 << 40, ExitThreshold: 1,
		RejectThreshold: 1 << 50, ReservationLead: 0}
	for _, history := range []int{0, 2000, 20000} {
		for _, live := range []int{50, 400} {
			b.Run(fmt.Sprintf("history=%d/live=%d", history, live), func(b *testing.B) {
				k, _ := New(cfg)
				var t int64
				for i := 0; i < history; i++ {
					id := fmt.Sprintf("h%d", i)
					if _, err := k.Admit(OrderRequest{ID: id, Kind: Immediate, Duration: 1}, t); err != nil {
						b.Fatal(err)
					}
					if err := k.Complete(id, t+1); err != nil {
						b.Fatal(err)
					}
					t += 2
				}
				for i := 0; i < live; i++ {
					id := fmt.Sprintf("l%d", i)
					if _, err := k.Admit(OrderRequest{ID: id, Kind: Immediate, Duration: 1 << 30}, t); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					id := fmt.Sprintf("probe%d", i)
					if _, err := k.Admit(OrderRequest{ID: id, Kind: Immediate, Duration: 1}, t); err != nil {
						b.Fatal(err)
					}
					if err := k.Cancel(id, t); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
