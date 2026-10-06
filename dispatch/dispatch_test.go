package dispatch

import (
	"errors"
	"sync"
	"testing"
)

func newMatrix(edges map[[2]string]int64) TravelMap {
	m := TravelMap{}
	for e, d := range edges {
		m.set(e[0], e[1], d)
		m.set(e[1], e[0], d)
	}
	return m
}

func (m TravelMap) set(from, to string, d int64) {
	row := m[from]
	if row == nil {
		row = map[string]int64{}
		m[from] = row
	}
	row[to] = d
}

func regRider(t *testing.T, d *Dispatcher, at int64, id, pos, region string, cap int) {
	t.Helper()
	if err := d.RegisterRider(at, Rider{ID: id, Region: region, Capacity: cap, Pos: pos, DepartedAt: at}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func submit(t *testing.T, d *Dispatcher, at int64, id, pk, dk string, ready, promise int64) {
	t.Helper()
	if err := d.SubmitOrder(at, Order{ID: id, Pickup: pk, Dropoff: dk, ReadyAt: ready, Promise: promise}); err != nil {
		t.Fatalf("submit %s: %v", id, err)
	}
}

// regionR 返回所有取货点同属一个区域 "R" 的配置（区域与耗时拓扑解耦）。
func regionR() func(Location) Location { return func(Location) Location { return "R" } }

// 承诺时刻恰好取等：新单送达推定 == promise 必须被接受。
func TestPromiseEquality(t *testing.T) {
	tt := newMatrix(map[[2]string]int64{
		{"H", "A"}: 10, {"A", "B"}: 20,
	})
	d := NewDispatcher(tt, Config{MaxDetour: 100})
	regRider(t, d, 0, "r1", "H", "A", 3)
	submit(t, d, 1, "o1", "A", "B", 0, 30)
	res, err := d.DispatchOrder(2, "o1")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Arrival != 30 || res.RiderID != "r1" {
		t.Fatalf("unexpected %+v", res)
	}
}

// 绕路上限恰好取等接受，超过 1 秒归类为在途违约。
func TestDetourEquality(t *testing.T) {
	cfg := Config{MaxDetour: 20, RegionOf: regionR()}
	d := NewDispatcher(newMatrix(map[[2]string]int64{
		{"H", "X"}: 0, {"X", "Y"}: 100,
		{"H", "A"}: 200, {"A", "Y"}: 60, {"Y", "B"}: 0, {"X", "A"}: 60,
	}), cfg)
	regRider(t, d, 0, "r1", "H", "R", 5)
	submit(t, d, 1, "old", "X", "Y", 0, 1000)
	if _, err := d.DispatchOrder(2, "old"); err != nil {
		t.Fatalf("dispatch old: %v", err)
	}
	// A 只能经 X->A(60)->Y(60) 抵达（H->A 200 会击穿新单承诺），
	// B 只可从 Y 到达，故取 A 必夹在 X 与 Y 之间，旧单送达被迫延后恰好 20。
	submit(t, d, 3, "new", "A", "B", 0, 1000)
	res, err := d.DispatchOrder(4, "new")
	if err != nil {
		t.Fatalf("detour exactly 20 should be accepted: %v", err)
	}
	if res.OldExtra != 20 {
		t.Fatalf("old extra = %d, want 20", res.OldExtra)
	}

	d2 := NewDispatcher(newMatrix(map[[2]string]int64{
		{"H2", "X2"}: 0, {"X2", "Y2"}: 100, {"X2", "D"}: 100,
		{"H2", "C"}: 200, {"X2", "C"}: 61, {"C", "Y2"}: 60, {"Y2", "D"}: 0,
	}), Config{MaxDetour: 20, RegionOf: regionR()})
	regRider(t, d2, 0, "r2", "H2", "R", 5)
	submit(t, d2, 1, "old2", "X2", "Y2", 0, 1000)
	if _, err := d2.DispatchOrder(2, "old2"); err != nil {
		t.Fatalf("dispatch old2: %v", err)
	}
	submit(t, d2, 3, "new2", "C", "D", 0, 1000)
	if _, err := d2.DispatchOrder(4, "new2"); !errors.Is(err, ErrExistingViolation) {
		t.Fatalf("detour 21 want ErrExistingViolation, got %v", err)
	}
}

// 早到等待使后续停靠延后；早于就绪的取货完成被抬到就绪时刻。
func TestWaitAtPickupDelaysLater(t *testing.T) {
	tt := newMatrix(map[[2]string]int64{
		{"H", "A"}: 5, {"A", "B"}: 10,
	})
	d := NewDispatcher(tt, Config{MaxDetour: 100})
	regRider(t, d, 0, "r1", "H", "A", 3)
	submit(t, d, 1, "o1", "A", "B", 100, 1000)
	if _, err := d.DispatchOrder(2, "o1"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	sched, _ := d.RiderSchedule("r1")
	if sched[0].Arrive != 5 || sched[0].Depart != 100 || sched[1].Arrive != 110 {
		t.Fatalf("sched = %+v", sched)
	}
	res, err := d.CompleteStop(50, "r1", "o1", StopPickup)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if res.Depart != 100 {
		t.Fatalf("depart = %d, want 100", res.Depart)
	}
}

// 三种无可行骑手原因可程序化区分。
func TestNoRiderReasons(t *testing.T) {
	tt := newMatrix(map[[2]string]int64{{"H", "A"}: 1, {"A", "B"}: 1, {"A", "Z"}: 1})
	d := NewDispatcher(tt, Config{MaxDetour: 100, RegionOf: regionR()})
	regRider(t, d, 0, "r1", "H", "R", 1)
	submit(t, d, 1, "hold", "A", "Z", 0, 1000)
	if _, err := d.DispatchOrder(2, "hold"); err != nil {
		t.Fatal(err)
	}
	submit(t, d, 3, "n1", "A", "B", 0, 1000)
	if _, err := d.DispatchOrder(4, "n1"); !errors.Is(err, ErrNoRiderRegionCapacity) {
		t.Fatalf("reason1 = %v", err)
	}

	d2 := NewDispatcher(newMatrix(map[[2]string]int64{
		{"H", "A"}: 10, {"A", "B"}: 100,
	}), Config{MaxDetour: 100, RegionOf: regionR()})
	regRider(t, d2, 0, "r1", "H", "R", 5)
	submit(t, d2, 1, "n2", "A", "B", 0, 50)
	if _, err := d2.DispatchOrder(2, "n2"); !errors.Is(err, ErrNewOrderPromise) {
		t.Fatalf("reason2 = %v", err)
	}

	d3 := NewDispatcher(newMatrix(map[[2]string]int64{
		{"H", "X"}: 0, {"X", "Y"}: 10, // 旧单 X->Y，10 秒送达
		// 取 A 只能在 X 之前（H->A=50）或 X 之后（X->A=5）；
		// 送 B 只能从 Y 以 0 耗时到达，故 A 必在 X 前且 B 在 Y 后，
		// 旧单 Y 被推到 50+5+10=65，既超承诺 10 也超绕路 2。
		{"H", "A"}: 50, {"X", "A"}: 5, {"A", "Y"}: 10, {"Y", "B"}: 0,
	}), Config{MaxDetour: 2, RegionOf: regionR()})
	regRider(t, d3, 0, "r1", "H", "R", 5)
	submit(t, d3, 1, "old3", "X", "Y", 0, 10)
	if _, err := d3.DispatchOrder(2, "old3"); err != nil {
		t.Fatal(err)
	}
	submit(t, d3, 3, "n3", "A", "B", 0, 1000)
	if _, err := d3.DispatchOrder(4, "n3"); !errors.Is(err, ErrExistingViolation) {
		t.Fatalf("reason3 = %v", err)
	}
}

// 平局规则逐层生效：增量相同 -> 持有数更少 -> ID 字典序。
func TestTieBreakers(t *testing.T) {
	d := NewDispatcher(newMatrix(map[[2]string]int64{
		{"Ha", "A"}: 10, {"A", "B"}: 10,
		{"Hz", "A"}: 10,
	}), Config{MaxDetour: 100, RegionOf: regionR()})
	regRider(t, d, 0, "z9", "Hz", "R", 5)
	regRider(t, d, 0, "a1", "Ha", "R", 5)
	submit(t, d, 1, "o", "A", "B", 0, 100)
	res, err := d.DispatchOrder(2, "o")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiderID != "a1" {
		t.Fatalf("lex tie want a1, got %s", res.RiderID)
	}

	d2 := NewDispatcher(newMatrix(map[[2]string]int64{
		{"HF", "A"}: 10, {"A", "B"}: 10,
		{"HB", "X"}: 0, {"X", "A"}: 10, {"X", "Z"}: 1,
	}), Config{MaxDetour: 100, RegionOf: regionR()})
	regRider(t, d2, 0, "busy", "HB", "R", 5)
	regRider(t, d2, 0, "free", "HF", "R", 5)
	submit(t, d2, 1, "held", "X", "Z", 0, 1000)
	if _, err := d2.DispatchOrder(2, "held"); err != nil {
		t.Fatal(err)
	}
	submit(t, d2, 3, "o2", "A", "B", 0, 100)
	res2, err := d2.DispatchOrder(4, "o2")
	if err != nil {
		t.Fatal(err)
	}
	if res2.RiderID != "free" {
		t.Fatalf("held tie want free, got %s", res2.RiderID)
	}
}

// 取消：不变晚成立、已取货不可取消、会变晚的取消被拒且不留痕。
func TestCancellation(t *testing.T) {
	tt := newMatrix(map[[2]string]int64{
		{"H", "A"}: 0, {"A", "B"}: 100, {"B", "C"}: 0, {"A", "C"}: 100,
	})
	d := NewDispatcher(tt, Config{MaxDetour: 1000, RegionOf: regionR()})
	regRider(t, d, 0, "r1", "H", "R", 5)
	submit(t, d, 1, "o1", "A", "C", 0, 1000)
	submit(t, d, 2, "o2", "B", "B", 0, 1000) // 同点取送，夹在 A->C 之间不增加里程
	if _, err := d.DispatchOrder(3, "o1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DispatchOrder(4, "o2"); err != nil {
		t.Fatal(err)
	}
	before, _ := d.RiderSchedule("r1")
	cBefore := before[len(before)-1].Arrive
	if err := d.CancelOrder(5, "o2"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	after, _ := d.RiderSchedule("r1")
	if after[len(after)-1].Arrive > cBefore {
		t.Fatalf("cancellation delayed C: %d > %d", after[len(after)-1].Arrive, cBefore)
	}
	if st, _ := d.OrderStatus("o2"); st != OrderCancelled {
		t.Fatalf("o2 status = %v", st)
	}
	if _, err := d.CompleteStop(6, "r1", "o1", StopPickup); err != nil {
		t.Fatal(err)
	}
	if err := d.CancelOrder(7, "o1"); !errors.Is(err, ErrOrderAlreadyPicked) {
		t.Fatalf("picked cancel = %v", err)
	}

	d2 := NewDispatcher(newMatrix(map[[2]string]int64{
		{"H", "A"}: 0, {"A", "P"}: 0, {"P", "Q"}: 0, {"Q", "B"}: 0,
		{"A", "B"}: 100,
	}), Config{MaxDetour: 1000, RegionOf: regionR()})
	regRider(t, d2, 0, "r2", "H", "R", 5)
	submit(t, d2, 1, "keep", "A", "B", 0, 1000)
	submit(t, d2, 2, "mid", "P", "Q", 0, 1000)
	if _, err := d2.DispatchOrder(3, "keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.DispatchOrder(4, "mid"); err != nil {
		t.Fatal(err)
	}
	if err := d2.CancelOrder(5, "mid"); !errors.Is(err, ErrCancelWouldDelay) {
		t.Fatalf("delay cancel = %v", err)
	}
	if st, _ := d2.OrderStatus("mid"); st != OrderAssigned {
		t.Fatalf("rejected cancel left status %v", st)
	}
	r2, _ := d2.RiderSnapshot("r2")
	if len(r2.Pending) != 4 {
		t.Fatalf("rejected cancel mutated sequence, len=%d", len(r2.Pending))
	}
}

// 乱序完成被拒且不留痕；时钟回退在状态错误之前、参数非法最先。
func TestRejectionOrderAndNoTrace(t *testing.T) {
	tt := newMatrix(map[[2]string]int64{
		{"H", "A"}: 1, {"A", "B"}: 1,
	})
	d := NewDispatcher(tt, Config{MaxDetour: 100})
	regRider(t, d, 10, "r1", "H", "A", 3)
	submit(t, d, 11, "o1", "A", "B", 0, 100)
	if _, err := d.DispatchOrder(12, "o1"); err != nil {
		t.Fatal(err)
	}

	rBefore, _ := d.RiderSnapshot("r1")
	// 报告第二个停靠 -> 乱序。
	if _, err := d.CompleteStop(13, "r1", "o1", StopDropoff); !errors.Is(err, ErrStopOutOfOrder) {
		t.Fatalf("out of order = %v", err)
	}
	rAfter, _ := d.RiderSnapshot("r1")
	if len(rAfter.Pending) != len(rBefore.Pending) || rAfter.DepartedAt != rBefore.DepartedAt || rAfter.Pos != rBefore.Pos {
		t.Fatal("out-of-order completion left a trace")
	}
	if st, _ := d.OrderStatus("o1"); st != OrderAssigned {
		t.Fatalf("status changed to %v", st)
	}
	if d.Now() != 12 {
		t.Fatalf("clock advanced to %d", d.Now())
	}

	// 时钟回退先于对象/状态判定。
	if err := d.CancelOrder(1, "missing"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback priority = %v", err)
	}
	// 参数非法最先。
	if err := d.SubmitOrder(0, Order{ID: ""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid priority = %v", err)
	}
	// 时钟合法后才报对象不存在。
	if err := d.CancelOrder(13, "missing"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("not found = %v", err)
	}
	// 重复派单。
	if _, err := d.DispatchOrder(13, "o1"); !errors.Is(err, ErrOrderAlreadyAssigned) {
		t.Fatalf("already assigned = %v", err)
	}
	// 离线骑手不接单（此处骑手无新单可派，改用 SetOnline 后完成被拒验证状态）。
	if err := d.SetOnline(13, "r1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CompleteStop(14, "r1", "o1", StopPickup); !errors.Is(err, ErrRiderOffline) {
		t.Fatalf("offline = %v", err)
	}
}

// 并发派单：容量上限不被突破，且每笔订单至多归属一位骑手。
func TestConcurrentDispatchCapacity(t *testing.T) {
	const capacity = 3
	const n = 40
	edges := map[[2]string]int64{}
	for i := 0; i < n; i++ {
		edges[[2]string{"H", fmtLoc("A", i)}] = 1
		// 任意取货点到任意送达点耗时 0：保证每笔订单单独插入都可行，
		// 容量成为唯一约束（缺省耗时源会把未知边视为不可达）。
		for j := 0; j < n; j++ {
			edges[[2]string{fmtLoc("A", i), fmtLoc("B", j)}] = 0
		}
	}
	tt := newMatrix(edges)
	d := NewDispatcher(tt, Config{MaxDetour: 1 << 40, RegionOf: regionR()})
	regRider(t, d, 0, "r1", "H", "R", capacity)

	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	accepted := make(chan string, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		oid := fmtLoc("o", i)
		// 先串行登记，避免并发下 SubmitOrder 与 DispatchOrder 的时钟互相回退。
		if err := d.SubmitOrder(1, Order{
			ID: oid, Pickup: fmtLoc("A", i), Dropoff: fmtLoc("B", i),
			ReadyAt: 0, Promise: 1 << 40,
		}); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// 所有派单携带同一时刻：只有容量个能在其串行位置上通过。
			res, err := d.DispatchOrder(2, oid)
			if err == nil {
				accepted <- res.RiderID
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent op: %v", err)
	}

	// 骑手序列中不同订单数恰好为 min(capacity, n)，且每个停靠成对有效。
	r, _ := d.RiderSnapshot("r1")
	if got := countHeld(r.Pending); got != capacity {
		t.Fatalf("held = %d, want capacity %d", got, capacity)
	}
	owners := map[string]int{}
	for _, s := range r.Pending {
		owners[s.OrderID]++
	}
	for oid, c := range owners {
		if c != 2 {
			t.Fatalf("order %s appears %d times", oid, c)
		}
	}
	if len(accepted) != capacity {
		t.Fatalf("accepted = %d, want %d", len(accepted), capacity)
	}
}

func fmtLoc(prefix string, i int) string { return prefix + "-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// countingSource 统计 Travel 查询次数，用于性能不变量的可验证证明。
type countingSource struct {
	calls int64
	edges map[[2]Location]int64
}

func (c *countingSource) Travel(from, to Location) (int64, bool) {
	c.calls++
	if d, ok := c.edges[[2]Location{from, to}]; ok {
		return d, true
	}
	return 1, true // 默认任意点间耗时 1
}

// 性能不变量 1：单骑手可行性开销只随未完成停靠数增长，
// 不随历史已完成停靠数 / 平台订单总量增长。
func TestPerRiderCostIndependentOfHistory(t *testing.T) {
	cs := &countingSource{edges: map[[2]Location]int64{}}
	d := NewDispatcher(cs, Config{MaxDetour: 1 << 40})
	regRider(t, d, 0, "r1", "S", "M", 100000)

	// 制造大量“历史已完成停靠 + 平台订单”，但当前未完成序列保持很短。
	for i := 0; i < 200; i++ {
		at := int64(1 + i*4)
		oid := fmtLoc("hist", i)
		if err := d.SubmitOrder(at, Order{ID: oid, Pickup: "M", Dropoff: "Z", Promise: 1 << 40}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.DispatchOrder(at+1, oid); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CompleteStop(at+2, "r1", oid, StopPickup); err != nil {
			t.Fatal(err)
		}
		if _, err := d.CompleteStop(at+3, "r1", oid, StopDropoff); err != nil {
			t.Fatal(err)
		}
	}

	cs.calls = 0
	order := Order{ID: "probe", Pickup: "M", Dropoff: "N", ReadyAt: 0, Promise: 1 << 40}
	r, _ := d.RiderSnapshot("r1")
	if len(r.Pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(r.Pending))
	}
	if _, has := BestInsertion(cs, &r, order, 0, 0, 1<<40, nil); !has {
		t.Fatal("probe infeasible")
	}
	smallHistoryCalls := cs.calls

	// 对比：当前未完成停靠很多时调用量应明显更大，证明开销与“未完成数”相关。
	cs.calls = 0
	r2 := &Rider{ID: "r2", Region: "M", Capacity: 100000, Pos: "S", DepartedAt: 0}
	for i := 0; i < 40; i++ {
		r2.Pending = append(r2.Pending,
			Stop{OrderID: fmtLoc("p", i), Kind: StopPickup, At: "M"},
			Stop{OrderID: fmtLoc("p", i), Kind: StopDropoff, At: "Z"},
		)
	}
	if _, has := BestInsertion(cs, r2, order, 0, 0, 1<<40, nil); !has {
		t.Fatal("large probe infeasible")
	}
	largePendingCalls := cs.calls
	if !(largePendingCalls > smallHistoryCalls*5) {
		t.Fatalf("cost not driven by pending size: history-only calls=%d, 40-pending calls=%d",
			smallHistoryCalls, largePendingCalls)
	}
	t.Logf("Travel calls: 0 pending after 200 completed orders=%d; 80 pending=%d",
		smallHistoryCalls, largePendingCalls)
}

// 性能不变量 2：为一笔新订单筛选候选不随其他区域骑手数增长。
func TestCandidateFilterIndependentOfOtherRegions(t *testing.T) {
	measure := func(otherRegions int) int64 {
		cs := &countingSource{edges: map[[2]Location]int64{}}
		d := NewDispatcher(cs, Config{MaxDetour: 1 << 40, RegionOf: func(Location) Location { return "R-target" }})
		regRider(t, d, 0, "target", "S", "R-target", 10)
		for i := 0; i < otherRegions; i++ {
			regRider(t, d, 0, fmtLoc("other", i), "S", fmtLoc("R", i), 10)
		}
		submit(t, d, 1, "o", "M0", "D", 0, 1<<40)
		cs.calls = 0
		if _, err := d.DispatchOrder(2, "o"); err != nil {
			t.Fatal(err)
		}
		return cs.calls
	}
	// 取货点 M0 属于 R-target；其他区域骑手数从 10 扩到 400，
	// 同区候选骑手恒为 1，Travel 查询次数必须不变。
	few := measure(10)
	many := measure(400)
	if few != many {
		t.Fatalf("candidate cost grew with other-region riders: %d vs %d", few, many)
	}
	t.Logf("Travel calls per dispatch with 10 vs 400 other-region riders: %d, %d", few, many)
}
