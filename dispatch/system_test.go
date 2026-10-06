package dispatch

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// tableSrc 是查表式耗时源，未配置的点对返回 def。
type tableSrc struct {
	m   map[[2]Point]int64
	def int64
}

func (ts tableSrc) TravelTime(a, b Point) int64 {
	if v, ok := ts.m[[2]Point{a, b}]; ok {
		return v
	}
	return ts.def
}

// legs 以 (from, to, seconds) 三元组构造查表耗时源。
func legs(def int64, triples ...any) tableSrc {
	m := make(map[[2]Point]int64)
	for i := 0; i+3 <= len(triples); i += 3 {
		m[[2]Point{Point(triples[i].(string)), Point(triples[i+1].(string))}] = triples[i+2].(int64)
	}
	return tableSrc{m: m, def: def}
}

func mustDispatch(t *testing.T, s *System, orderID string, at int64) Assignment {
	t.Helper()
	asg, err := s.DispatchOrder(orderID, at)
	if err != nil {
		t.Fatalf("dispatch %s: unexpected error %v", orderID, err)
	}
	return asg
}

func mustRoute(t *testing.T, s *System, riderID string) []StopView {
	t.Helper()
	sv, err := s.Route(riderID)
	if err != nil {
		t.Fatalf("route %s: %v", riderID, err)
	}
	return sv
}

// 承诺时刻恰好取等：送达推定 == 承诺时刻时必须接受。
func TestPromiseExactlyMet(t *testing.T) {
	src := legs(1000, "H", "P", int64(10), "P", "D", int64(20))
	s := New(Config{MaxDetour: 100}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 2}, 0); err != nil {
		t.Fatal(err)
	}
	// 送达推定 = 10(行程) + 5(取货停留) + 20(行程) = 35。
	o := Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 35, PickupDwell: 5}
	if err := s.CreateOrder(o, 1); err != nil {
		t.Fatal(err)
	}
	asg := mustDispatch(t, s, "o1", 2)
	if asg.DropEta != 35 {
		t.Fatalf("DropEta = %d, want 35", asg.DropEta)
	}
	// 承诺少 1 秒则不可行。
	o2 := o
	o2.ID = "o2"
	o2.PromiseAt = 34
	if err := s.CreateOrder(o2, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchOrder("o2", 4); !errors.Is(err, ErrNoPromisePosition) {
		t.Fatalf("err = %v, want ErrNoPromisePosition", err)
	}
}

// 绕路上限恰好取等：在途订单新增延后 == 单次绕路上限时必须接受，超 1 秒拒绝。
func TestDetourExactlyMet(t *testing.T) {
	build := func(aToD int64) *System {
		src := legs(1000,
			"H", "A", int64(5), "A", "B", int64(10),
			"H", "C", int64(1), "C", "A", int64(1),
			"A", "D", aToD, "D", "B", int64(3))
		s := New(Config{MaxDetour: 10}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 3}, 0); err != nil {
			t.Fatal(err)
		}
		// o1: H->A=5, A->B=10，送达推定 15，承诺宽松。
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "A", Drop: "B", ReadyAt: 0, PromiseAt: 1000}, 1); err != nil {
			t.Fatal(err)
		}
		mustDispatch(t, s, "o1", 2)
		// o2: 唯一可行位置 [C,A,D,B]，o1 新送达 = 1+1+aToD+3。
		if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "C", Drop: "D", ReadyAt: 0, PromiseAt: 500}, 3); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// A->D=20：o1 新送达 = 25，延后 25-15 = 10 == 绕路上限，必须接受。
	s := build(20)
	asg := mustDispatch(t, s, "o2", 4)
	if asg.PickupPos != 0 || asg.DeliverPos != 2 {
		t.Fatalf("pos = (%d,%d), want (0,2)", asg.PickupPos, asg.DeliverPos)
	}
	route := mustRoute(t, s, "r1")
	if route[3].OrderID != "o1" || route[3].Eta != 25 {
		t.Fatalf("o1 eta = %+v, want 25 at last stop", route)
	}
	// A->D=21：延后 11 > 10，拒绝且报绕路原因。
	s2 := build(21)
	if _, err := s2.DispatchOrder("o2", 4); !errors.Is(err, ErrDetourLimit) {
		t.Fatalf("err = %v, want ErrDetourLimit", err)
	}
}

// 早到等待：取货早到须等至出餐就绪，后续停靠推定随之延后。
func TestEarlyArrivalWaits(t *testing.T) {
	src := legs(1000,
		"H", "P", int64(10), "P", "D", int64(10),
		"D", "P2", int64(1), "P2", "D2", int64(1))
	s := New(Config{MaxDetour: 100}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 3}, 0); err != nil {
		t.Fatal(err)
	}
	// 10 时刻到达取货点，出餐就绪 100，离开 = 100+5 = 105。
	if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 100, PromiseAt: 10000, PickupDwell: 5}, 1); err != nil {
		t.Fatal(err)
	}
	mustDispatch(t, s, "o1", 2)
	route := mustRoute(t, s, "r1")
	if route[0].Eta != 10 || route[0].Leave != 105 {
		t.Fatalf("pickup eta/leave = %d/%d, want 10/105", route[0].Eta, route[0].Leave)
	}
	if route[1].Eta != 115 {
		t.Fatalf("deliver eta = %d, want 115", route[1].Eta)
	}
	// 后续订单的推定必须建立在等待后的 115 之上。
	if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "P2", Drop: "D2", ReadyAt: 0, PromiseAt: 10000}, 3); err != nil {
		t.Fatal(err)
	}
	asg := mustDispatch(t, s, "o2", 4)
	if asg.DropEta != 117 {
		t.Fatalf("o2 DropEta = %d, want 117 (wait propagated)", asg.DropEta)
	}
}

// 三种无可行骑手原因，按次序只报一种。
func TestNoFeasibleRiderReasons(t *testing.T) {
	// 原因一：所有在区骑手载量已满。
	t.Run("capacity", func(t *testing.T) {
		src := legs(1)
		s := New(Config{MaxDetour: 100}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 1}, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, 1); err != nil {
			t.Fatal(err)
		}
		mustDispatch(t, s, "o1", 2)
		if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DispatchOrder("o2", 4); !errors.Is(err, ErrNoRiderCapacity) {
			t.Fatalf("err = %v, want ErrNoRiderCapacity", err)
		}
	})
	// 原因二：有余量但任何位置都满足不了新订单自身承诺。
	t.Run("promise", func(t *testing.T) {
		src := legs(1000, "H", "P", int64(10), "P", "D", int64(20))
		s := New(Config{MaxDetour: 100}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 2}, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 29}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DispatchOrder("o1", 2); !errors.Is(err, ErrNoPromisePosition) {
			t.Fatalf("err = %v, want ErrNoPromisePosition", err)
		}
	})
	// 原因三：有满足新订单承诺的位置，但都会让在途订单超承诺或超绕路。
	t.Run("detour", func(t *testing.T) {
		src := legs(1000,
			"H", "A", int64(5), "A", "B", int64(10),
			"H", "C", int64(1), "C", "A", int64(1),
			"A", "D", int64(21), "D", "B", int64(3))
		s := New(Config{MaxDetour: 10}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 3}, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "A", Drop: "B", ReadyAt: 0, PromiseAt: 1000}, 1); err != nil {
			t.Fatal(err)
		}
		mustDispatch(t, s, "o1", 2)
		if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "C", Drop: "D", ReadyAt: 0, PromiseAt: 500}, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DispatchOrder("o2", 4); !errors.Is(err, ErrDetourLimit) {
			t.Fatalf("err = %v, want ErrDetourLimit", err)
		}
	})
}

// 骑手选择平局规则逐层生效：增量相同 -> 持有更少 -> 标识字典序更小。
func TestRiderSelectionTieBreaks(t *testing.T) {
	src := legs(1000,
		"H", "X", int64(5), "X", "Y", int64(5),
		"H", "P", int64(5), "P", "D", int64(5),
		"Y", "P", int64(5), "D", "P2", int64(5),
		"H", "P2", int64(5), "P2", "D2", int64(5), "Y", "P2", int64(5))
	s := New(Config{MaxDetour: 1000000}, src)
	mk := func(o Order, at int64) {
		if err := s.CreateOrder(o, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddRider(Rider{ID: "rA", Region: "R", Pos: "H", DepartAt: 0, Capacity: 5}, 0); err != nil {
		t.Fatal(err)
	}
	// rA 先持有一单：序列 [X,Y]，总耗时 10。
	mk(Order{ID: "o0", Region: "R", Pickup: "X", Drop: "Y", ReadyAt: 0, PromiseAt: 100000}, 1)
	mustDispatch(t, s, "o0", 2)
	if err := s.AddRider(Rider{ID: "rB", Region: "R", Pos: "H", DepartAt: 0, Capacity: 5}, 3); err != nil {
		t.Fatal(err)
	}
	// o1：rA 追加 [X,Y,P,D] 增量 10；rB 空序列 [P,D] 增量 10。平局 -> rB 持有更少。
	mk(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100000}, 4)
	asg := mustDispatch(t, s, "o1", 5)
	if asg.RiderID != "rB" || asg.Increment != 10 {
		t.Fatalf("asg = %+v, want rider rB inc 10", asg)
	}
	// o2：两骑手增量、持有数都相同 -> 字典序 rA < rB。
	mk(Order{ID: "o2", Region: "R", Pickup: "P2", Drop: "D2", ReadyAt: 0, PromiseAt: 100000}, 6)
	asg = mustDispatch(t, s, "o2", 7)
	if asg.RiderID != "rA" {
		t.Fatalf("asg.RiderID = %s, want rA (lexicographic tie-break)", asg.RiderID)
	}
}

// 位置选择平局规则逐层生效：在途总延后相同 -> 新订单送达最早 -> 取货更靠前。
func TestPositionSelectionTieBreaks(t *testing.T) {
	// 第一层：两个位置对在途订单延后量相同，取新订单送达更早的 (0,1)。
	t.Run("earliest drop wins", func(t *testing.T) {
		src := legs(1000,
			"H", "A", int64(5), "A", "B", int64(5),
			"H", "C", int64(2), "C", "D", int64(2),
			"D", "A", int64(1), "A", "C", int64(1), "D", "B", int64(1))
		s := New(Config{MaxDetour: 1000000}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 5}, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "A", Drop: "B", ReadyAt: 0, PromiseAt: 100000}, 1); err != nil {
			t.Fatal(err)
		}
		mustDispatch(t, s, "o1", 2)
		if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "C", Drop: "D", ReadyAt: 0, PromiseAt: 100000}, 3); err != nil {
			t.Fatal(err)
		}
		asg := mustDispatch(t, s, "o2", 4)
		// (0,1): o2 送达 4；(1,2): o2 送达 8；两者对在途延后均为 0。
		if asg.PickupPos != 0 || asg.DeliverPos != 1 || asg.DropEta != 4 {
			t.Fatalf("asg = %+v, want (0,1) eta 4", asg)
		}
	})
	// 第二层：总延后与送达时刻都相同，取取货位置更靠前的 (0,2) 而非 (1,2)。
	t.Run("earlier pickup wins", func(t *testing.T) {
		src := legs(1000,
			"H", "A", int64(0), "A", "B", int64(5),
			"H", "C", int64(0), "C", "A", int64(0), "A", "C", int64(0),
			"A", "D", int64(3), "C", "D", int64(3), "D", "B", int64(0))
		s := New(Config{MaxDetour: 1000000}, src)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 5}, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "A", Drop: "B", ReadyAt: 0, PromiseAt: 100000}, 1); err != nil {
			t.Fatal(err)
		}
		mustDispatch(t, s, "o1", 2)
		if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "C", Drop: "D", ReadyAt: 0, PromiseAt: 100000}, 3); err != nil {
			t.Fatal(err)
		}
		asg := mustDispatch(t, s, "o2", 4)
		// (0,2) 与 (1,2)：在途延后、o2 送达时刻(3)均相同，取 p 更小者。
		if asg.PickupPos != 0 || asg.DeliverPos != 2 || asg.DropEta != 3 {
			t.Fatalf("asg = %+v, want (0,2) eta 3", asg)
		}
	})
}

// 取消后任何在途订单的推定到达时刻不得变晚（耗时源不满足三角不等式时须钳制）。
func TestCancelNeverIncreasesEta(t *testing.T) {
	// H->C=100 但 H->A->B->C=3：移除 A、B 后裸重算会变晚，必须钳制。
	src := legs(1000,
		"H", "A", int64(1), "A", "B", int64(1), "B", "C", int64(1),
		"H", "C", int64(100))
	s := New(Config{MaxDetour: 1000000}, src)
	// 直接构造：o1 在途（取+送），o2 已取货（仅剩送达停靠 C）。
	s.orders["o1"] = &orderState{
		order:  Order{ID: "o1", Region: "R", Pickup: "A", Drop: "B", ReadyAt: 0, PromiseAt: 100000},
		status: statusAssigned, rider: "r1",
	}
	s.orders["o2"] = &orderState{
		order:  Order{ID: "o2", Region: "R", Pickup: "B", Drop: "C", ReadyAt: 0, PromiseAt: 100000},
		status: statusPicked, rider: "r1",
	}
	s.riders["r1"] = &riderState{
		id: "r1", region: "R", pos: "H", departAt: 0, capacity: 5, online: true, held: 2,
		stops: []stop{
			{orderID: "o1", kind: StopPickup, etaCap: noEtaCap},
			{orderID: "o1", kind: StopDeliver, etaCap: noEtaCap},
			{orderID: "o2", kind: StopDeliver, etaCap: noEtaCap},
		},
	}
	s.byRegion["R"] = map[string]struct{}{"r1": {}}
	route := mustRoute(t, s, "r1")
	if len(route) != 3 || route[2].Eta != 3 {
		t.Fatalf("route = %+v, want o2 deliver eta 3", route)
	}
	// 取消 o1 移除其两个停靠；裸重算 o2 送达 = 100，必须钳制不变晚。
	if err := s.CancelOrder("o1", 1); err != nil {
		t.Fatal(err)
	}
	route = mustRoute(t, s, "r1")
	if len(route) != 1 {
		t.Fatalf("route = %+v, want 1 stop", route)
	}
	if route[0].Eta != 3 {
		t.Fatalf("o2 deliver eta = %d, must be clamped to pre-cancel 3 (raw 100)", route[0].Eta)
	}
}

// 乱序完成被拒且不留痕：状态与时钟都不变。
func TestOutOfOrderCompletionRejected(t *testing.T) {
	src := legs(1)
	s := New(Config{MaxDetour: 10}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 3}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, 10); err != nil {
		t.Fatal(err)
	}
	mustDispatch(t, s, "o1", 10)
	before := mustRoute(t, s, "r1")
	// 首停靠是取货，报送达完成 -> 乱序。
	if err := s.CompleteStop("r1", "o1", StopDeliver, 50); !errors.Is(err, ErrStopOutOfOrder) {
		t.Fatalf("err = %v, want ErrStopOutOfOrder", err)
	}
	after := mustRoute(t, s, "r1")
	if len(before) != len(after) || before[0] != after[0] || before[1] != after[1] {
		t.Fatalf("route changed after rejected op: %+v -> %+v", before, after)
	}
	// 时钟未推进到 50：t=10 的操作仍被接受。
	if err := s.CreateOrder(Order{ID: "o2", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, 10); err != nil {
		t.Fatalf("clock advanced by rejected op: %v", err)
	}
}

// 拒绝次序：参数非法 -> 时钟回退 -> 对象不存在或状态不符。
func TestRejectionOrder(t *testing.T) {
	src := legs(1)
	s := New(Config{MaxDetour: 10}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 1}, 10); err != nil {
		t.Fatal(err)
	}
	// 参数非法 + 时钟回退同时命中 -> 报参数非法。
	if _, err := s.DispatchOrder("", 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	// 时钟回退 + 订单不存在 -> 报时钟回退。
	if _, err := s.DispatchOrder("ghost", 5); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	// 仅订单不存在 -> 报对象不存在。
	if _, err := s.DispatchOrder("ghost", 15); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
}

// 时钟回退：被拒绝且时钟不变；等于当前最大时刻可以接受。
func TestClockRegression(t *testing.T) {
	src := legs(1)
	s := New(Config{MaxDetour: 10}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 1}, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOnline("r1", false, 9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	if err := s.SetOnline("r1", false, 10); err != nil {
		t.Fatalf("equal time must be accepted: %v", err)
	}
}

// 已取货订单不可取消，报已取货。
func TestCancelPickedOrderRejected(t *testing.T) {
	src := legs(1)
	s := New(Config{MaxDetour: 10}, src)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 2}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateOrder(Order{ID: "o1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, 1); err != nil {
		t.Fatal(err)
	}
	mustDispatch(t, s, "o1", 2)
	if err := s.CompleteStop("r1", "o1", StopPickup, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelOrder("o1", 4); !errors.Is(err, ErrOrderAlreadyPickedUp) {
		t.Fatalf("err = %v, want ErrOrderAlreadyPickedUp", err)
	}
}

// 并发派单：载量上限不被突破，每笔订单至多属于一位骑手。
func TestConcurrentDispatchCapacity(t *testing.T) {
	src := legs(1)
	s := New(Config{MaxDetour: 1000000}, src)
	for i, id := range []string{"r0", "r1", "r2"} {
		if err := s.AddRider(Rider{ID: id, Region: "R", Pos: "H", DepartAt: 0, Capacity: 2}, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	const orders = 30
	for i := 0; i < orders; i++ {
		o := Order{ID: "o" + string(rune('a'+i)), Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100000}
		if err := s.CreateOrder(o, int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < orders; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := s.DispatchOrder(id, 100); err == nil {
				okCount.Add(1)
			}
		}("o" + string(rune('a'+i)))
	}
	// 同一订单并发派两次：至多成功一次。
	dup := make(chan error, 2)
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DispatchOrder("oa", 100)
			dup <- err
		}()
	}
	wg.Wait()
	close(dup)
	if got := okCount.Load(); got > 6 {
		t.Fatalf("accepted %d orders, capacity total is 6", got)
	}
	dupOK := 0
	for err := range dup {
		if err == nil {
			dupOK++
		} else if !errors.Is(err, ErrOrderAlreadyAssigned) && !errors.Is(err, ErrNoRiderCapacity) {
			t.Fatalf("unexpected duplicate dispatch error: %v", err)
		}
	}
	if dupOK > 1 {
		t.Fatalf("same order assigned %d times", dupOK)
	}
	for _, id := range []string{"r0", "r1", "r2"} {
		held, err := s.Held(id)
		if err != nil {
			t.Fatal(err)
		}
		if held > 2 {
			t.Fatalf("rider %s holds %d orders, capacity 2", id, held)
		}
	}
}

// countingSrc 统计耗时查询次数，用于证明性能特性。
type countingSrc struct {
	inner TravelTimeSource
	n     atomic.Int64
}

func (c *countingSrc) TravelTime(a, b Point) int64 {
	c.n.Add(1)
	return c.inner.TravelTime(a, b)
}

func (c *countingSrc) reset() { c.n.Store(0) }
func (c *countingSrc) count() int64 {
	return c.n.Load()
}

// 单骑手可行性判定开销与历史已完成停靠数、平台订单总量无关。
func TestFeasibilityCostIndependentOfHistory(t *testing.T) {
	build := func(completed int, extraOrders int) (*System, *countingSrc) {
		cs := &countingSrc{inner: legs(1)}
		s := New(Config{MaxDetour: 1000000}, cs)
		if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 1000}, 0); err != nil {
			t.Fatal(err)
		}
		// 制造 completed 个已完成停靠的历史。
		for i := 0; i < completed/2; i++ {
			id := "h" + string(rune(i%26+'a')) + string(rune(i/26+'a'))
			o := Order{ID: id, Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}
			if err := s.CreateOrder(o, int64(2*i+1)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DispatchOrder(id, int64(2*i+1)); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteStop("r1", id, StopPickup, int64(2*i+2)); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteStop("r1", id, StopDeliver, int64(2*i+2)); err != nil {
				t.Fatal(err)
			}
		}
		// 制造平台历史订单总量（已取消）。
		for i := 0; i < extraOrders; i++ {
			id := "x" + string(rune(i%26+'a')) + string(rune(i/26+'a'))
			if err := s.CreateOrder(Order{ID: id, Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 100}, int64(1000+i)); err != nil {
				t.Fatal(err)
			}
			if err := s.CancelOrder(id, int64(1000+i)); err != nil {
				t.Fatal(err)
			}
		}
		// 当前在途 2 单（4 个未完成停靠）。
		for _, id := range []string{"c1", "c2"} {
			if err := s.CreateOrder(Order{ID: id, Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}, 2000); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DispatchOrder(id, 2000); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateOrder(Order{ID: "new", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}, 2001); err != nil {
			t.Fatal(err)
		}
		return s, cs
	}
	s1, c1 := build(0, 0)
	s2, c2 := build(400, 500)
	c1.reset()
	if _, err := s1.DispatchOrder("new", 2002); err != nil {
		t.Fatal(err)
	}
	c2.reset()
	if _, err := s2.DispatchOrder("new", 2002); err != nil {
		t.Fatal(err)
	}
	if c1.count() != c2.count() {
		t.Fatalf("feasibility cost grew with history: fresh=%d historical=%d", c1.count(), c2.count())
	}
}

// 候选骑手筛选开销与其他区域的骑手数量无关。
func TestCandidateFilterIndependentOfOtherRegions(t *testing.T) {
	cs := &countingSrc{inner: legs(1)}
	s := New(Config{MaxDetour: 1000000}, cs)
	if err := s.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 100}, 0); err != nil {
		t.Fatal(err)
	}
	mk := func(id string, at int64) {
		if err := s.CreateOrder(Order{ID: id, Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}, at); err != nil {
			t.Fatal(err)
		}
	}
	mk("o1", 1)
	cs.reset()
	if _, err := s.DispatchOrder("o1", 2); err != nil {
		t.Fatal(err)
	}
	// 注入大量其他区域骑手。
	for i := 0; i < 300; i++ {
		id := "z" + string(rune(i%26+'a')) + string(rune(i/26+'a'))
		if err := s.AddRider(Rider{ID: id, Region: "OTHER", Pos: "H", DepartAt: 0, Capacity: 5}, int64(3+i)); err != nil {
			t.Fatal(err)
		}
	}
	mk("o2", 400)
	cs.reset()
	if _, err := s.DispatchOrder("o2", 401); err != nil {
		t.Fatal(err)
	}
	after := cs.count()
	// 对照组：相同在途规模、无他区骑手，查询数必须完全一致。
	cs2 := &countingSrc{inner: legs(1)}
	s2 := New(Config{MaxDetour: 1000000}, cs2)
	if err := s2.AddRider(Rider{ID: "r1", Region: "R", Pos: "H", DepartAt: 0, Capacity: 100}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s2.CreateOrder(Order{ID: "p1", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.DispatchOrder("p1", 2); err != nil {
		t.Fatal(err)
	}
	if err := s2.CreateOrder(Order{ID: "p2", Region: "R", Pickup: "P", Drop: "D", ReadyAt: 0, PromiseAt: 1000000}, 3); err != nil {
		t.Fatal(err)
	}
	cs2.reset()
	if _, err := s2.DispatchOrder("p2", 4); err != nil {
		t.Fatal(err)
	}
	if cs2.count() != after {
		t.Fatalf("other-region riders changed candidate cost: with=%d without=%d", after, cs2.count())
	}
}
