package carpool

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// 新乘客并入后已有乘客的预计应付下降，但其锁价上限保持不变。
func TestLockPriceDropOnJoin(t *testing.T) {
	s := mustService(t, testCfg()) // 单价 1
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "a", 0, 100, 1, 1000, 10000, 1)
	if v := mustQuery(t, s, "a"); v.Cap != 100 || v.EstimatedFare != 100 {
		t.Fatalf("a alone: %+v, want cap=est=100", v)
	}
	mustSubmit(t, s, "b", 20, 80, 1, 1000, 10000, 2)
	v := mustQuery(t, s, "a")
	// 段 0-20、80-100 独行各 20，段 20-80 两人等分各 30，合计 70。
	if v.EstimatedFare != 70 {
		t.Fatalf("a after b joins: est=%d, want 70", v.EstimatedFare)
	}
	if v.Cap != 100 {
		t.Fatalf("a cap changed to %d, want unchanged 100", v.Cap)
	}
}

// 取消导致的涨价被先并入乘客的锁价上限截住。
func TestCancelRiseCappedByLock(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "b", 20, 80, 1, 1000, 10000, 1) // b 先并入，独行 60
	mustSubmit(t, s, "a", 0, 100, 1, 1000, 10000, 2) // a 并入时与 b 共享，上限锁为 70
	if v := mustQuery(t, s, "a"); v.Cap != 70 {
		t.Fatalf("a cap = %d, want 70", v.Cap)
	}
	if err := s.CancelOrder("b", 3); err != nil {
		t.Fatalf("cancel b: %v", err)
	}
	v := mustQuery(t, s, "a")
	if v.EstimatedFare != 70 {
		t.Fatalf("a after b cancels: est=%d, want 70 (rise capped by lock)", v.EstimatedFare)
	}
	if vb := mustQuery(t, s, "b"); vb.Status != StatusCancelled || vb.EstimatedFare != 5 {
		t.Fatalf("b view = %+v, want Cancelled with cancel fee 5", vb)
	}
}

// 等待订单在车辆位置更新时被重新尝试并成功匹配。
func TestWaitingMatchedOnPositionUpdate(t *testing.T) {
	s := mustService(t, testCfg()) // 停靠 10，每里程 1 时刻
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "a", 10, 20, 1, 1000, 10000, 0)
	// b 的预计上车时刻 = 0 + 100 + 2*10 = 120 > 100，进入等待。
	if r := mustSubmit(t, s, "b", 100, 200, 1, 1000, 100, 0); r.Status != StatusWaiting {
		t.Fatalf("b: got %v, want Waiting", r.Status)
	}
	// 车辆到达 20，a 完成；此后 b 的预计上车时刻 = 20 + 80 = 100，恰可匹配。
	events, err := s.UpdateVehiclePosition("v", 20, 20)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	var completed, matched bool
	for _, e := range events {
		if e.Kind == EventCompleted && e.OrderID == "a" {
			completed = true
		}
		if e.Kind == EventMatched && e.OrderID == "b" && e.VehicleID == "v" {
			matched = true
		}
	}
	if !completed || !matched {
		t.Fatalf("events = %v, want a Completed and b Matched", events)
	}
	if v := mustQuery(t, s, "b"); v.Status != StatusMatched && v.Status != StatusOnboard {
		t.Fatalf("b status = %v", v.Status)
	}
}

// 等待订单恰在最晚上车时刻仍未匹配即失效。
func TestWaitingExpiresAtLatestPickup(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	// 预计上车时刻 100 > 最晚 50，进入等待。
	if r := mustSubmit(t, s, "b", 100, 200, 1, 1000, 50, 0); r.Status != StatusWaiting {
		t.Fatalf("b: got %v, want Waiting", r.Status)
	}
	if _, err := s.UpdateVehiclePosition("v", 0, 49); err != nil {
		t.Fatalf("update t=49: %v", err)
	}
	if v := mustQuery(t, s, "b"); v.Status != StatusWaiting {
		t.Fatalf("b at t=49: %v, want still Waiting", v.Status)
	}
	events, err := s.UpdateVehiclePosition("v", 0, 50)
	if err != nil {
		t.Fatalf("update t=50: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventExpired || events[0].OrderID != "b" {
		t.Fatalf("events = %v, want b Expired exactly at latest pickup", events)
	}
	if v := mustQuery(t, s, "b"); v.Status != StatusExpired {
		t.Fatalf("b status = %v, want Expired", v.Status)
	}
}

// 上车后不可取消；完成后取消报订单已完成。
func TestCancelOnboardAndCompleted(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "a", 0, 10, 1, 100, 1000, 1) // 上车点即车辆位置，立即上车
	if err := s.CancelOrder("a", 2); !errors.Is(err, ErrOrderOnboard) {
		t.Fatalf("cancel onboard: got %v, want ErrOrderOnboard", err)
	}
	if _, err := s.UpdateVehiclePosition("v", 10, 3); err != nil {
		t.Fatalf("update: %v", err)
	}
	if v := mustQuery(t, s, "a"); v.Status != StatusCompleted || v.EstimatedFare != 10 {
		t.Fatalf("a view = %+v, want Completed fare 10", v)
	}
	if err := s.CancelOrder("a", 4); !errors.Is(err, ErrOrderCompleted) {
		t.Fatalf("cancel completed: got %v, want ErrOrderCompleted", err)
	}
}

// 时钟回退与位置倒退被拒绝，且被拒绝的操作不改变时钟与状态。
func TestClockRollbackAndPositionRegression(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 5, 0)
	mustSubmit(t, s, "a", 10, 20, 1, 100, 1000, 10)
	if err := s.CancelOrder("a", 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("cancel at t=9: got %v, want ErrClockRollback", err)
	}
	// 被拒绝后时钟未前进：t=10 的操作仍可接受。
	if err := s.CancelOrder("a", 10); err != nil {
		t.Fatalf("cancel at t=10 after rejected op: %v", err)
	}
	if _, err := s.UpdateVehiclePosition("v", 4, 11); !errors.Is(err, ErrPositionRegression) {
		t.Fatalf("regression: got %v, want ErrPositionRegression", err)
	}
	// 位置倒退被拒绝后位置不变。
	if _, err := s.UpdateVehiclePosition("v", 5, 12); err != nil {
		t.Fatalf("update to same pos after rejected regression: %v", err)
	}
}

// 错误只报次序最靠前的一类。
func TestErrorPrecedence(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 2, 0, 10)
	// 参数非法先于时钟回退。
	if err := s.CancelOrder("", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty id + rollback: got %v, want ErrInvalidParam", err)
	}
	// 时钟回退先于订单不存在。
	if err := s.CancelOrder("missing", 0); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("missing + rollback: got %v, want ErrClockRollback", err)
	}
	// 时钟回退先于车辆不存在。
	if _, err := s.UpdateVehiclePosition("missing", 1, 0); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("missing vehicle + rollback: got %v, want ErrClockRollback", err)
	}
	// 车辆不存在先于位置倒退。
	if _, err := s.UpdateVehiclePosition("missing", 1, 10); !errors.Is(err, ErrVehicleNotFound) {
		t.Fatalf("missing vehicle: got %v, want ErrVehicleNotFound", err)
	}
	// 订单不存在。
	if err := s.CancelOrder("missing", 10); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("missing order: got %v, want ErrOrderNotFound", err)
	}
	// 车辆前进到 5，使上车点 0 落后于车辆位置。
	if _, err := s.UpdateVehiclePosition("v", 5, 10); err != nil {
		t.Fatalf("advance vehicle: %v", err)
	}
	// 无车可用且无法等待（最晚时刻即当前时刻）。
	if _, err := s.SubmitOrder("o", 0, 10, 1, 0, 10, 10); !errors.Is(err, ErrNoVehicle) {
		t.Fatalf("no vehicle & latest==now: got %v, want ErrNoVehicle", err)
	}
	// 参数非法：下车点不大于上车点。
	if _, err := s.SubmitOrder("o2", 10, 10, 1, 0, 100, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("dropoff<=pickup: got %v, want ErrInvalidParam", err)
	}
}

// 配置与车辆注册的参数校验。
func TestConfigAndVehicleValidation(t *testing.T) {
	if _, err := NewService(Config{MaxActiveOrders: 0}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("MaxActiveOrders=0: got %v, want ErrInvalidParam", err)
	}
	if _, err := NewService(Config{StopDuration: -1, MaxActiveOrders: 1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative stop duration: got %v, want ErrInvalidParam", err)
	}
	s := mustService(t, testCfg())
	if err := s.AddVehicle("", 1, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty vehicle id: got %v, want ErrInvalidParam", err)
	}
	if err := s.AddVehicle("v", 0, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("seats=0: got %v, want ErrInvalidParam", err)
	}
	if err := s.AddVehicle("v", 1, -1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative pos: got %v, want ErrInvalidParam", err)
	}
	mustAddVehicle(t, s, "v", 1, 0, 5)
	if err := s.AddVehicle("v", 1, 0, 6); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("duplicate vehicle: got %v, want ErrInvalidParam", err)
	}
	if err := s.AddVehicle("w", 1, 0, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("add vehicle rollback: got %v, want ErrClockRollback", err)
	}
	// 重复订单标识。
	mustSubmit(t, s, "o", 0, 10, 1, 0, 100, 6)
	if _, err := s.SubmitOrder("o", 0, 10, 1, 0, 100, 6); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("duplicate order: got %v, want ErrInvalidParam", err)
	}
	// 查询不存在的订单。
	if _, err := s.QueryOrder("missing"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("query missing: got %v, want ErrOrderNotFound", err)
	}
}

// 查询开销与车辆历史订单总数无关：
// 完成订单即时结算归档，活跃查询只遍历在途订单（受 MaxActiveOrders 限制）。
func TestQueryCostIndependentOfHistory(t *testing.T) {
	cfg := Config{StopDuration: 1, TimePerDistance: 1, UnitPrice: 2, CancelFee: 5, MaxActiveOrders: 2}
	s := mustService(t, cfg)
	mustAddVehicle(t, s, "v", 4, 0, 0)
	var now, pos int64
	const history = 1000
	for i := 0; i < history; i++ {
		now++
		mustSubmit(t, s, fmt.Sprintf("h%d", i), pos, pos+10, 1, 0, now+10000, now)
		now++
		pos += 10
		if _, err := s.UpdateVehiclePosition("v", pos, now); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}
	now++
	mustSubmit(t, s, "cur", pos, pos+10, 1, 0, now+10000, now)
	// 完成订单的查询不触发任何分摊迭代。
	s.fareIters = 0
	if _, err := s.QueryOrder("h0"); err != nil {
		t.Fatalf("query completed: %v", err)
	}
	if s.fareIters != 0 {
		t.Fatalf("completed-order query iterated %d times, want 0", s.fareIters)
	}
	// 在途订单的查询迭代数只与在途订单数有关，与 1000 条历史无关。
	s.fareIters = 0
	if _, err := s.QueryOrder("cur"); err != nil {
		t.Fatalf("query active: %v", err)
	}
	bound := int64(2 * (2 * cfg.MaxActiveOrders) * (2 * cfg.MaxActiveOrders))
	if s.fareIters > bound {
		t.Fatalf("active-order query iterated %d times, exceeds active-set bound %d", s.fareIters, bound)
	}
	t.Logf("history=%d, active query iterations=%d (bound %d), completed query iterations=0",
		history, s.fareIters, bound)
}

// 并发调用等价于某个串行顺序：服务内部串行化，结束后不变式成立。
func TestConcurrentOperations(t *testing.T) {
	cfg := Config{StopDuration: 2, TimePerDistance: 1, UnitPrice: 3, CancelFee: 4, MaxActiveOrders: 4}
	s := mustService(t, cfg)
	vehicles := []string{"v0", "v1", "v2"}
	for i, vid := range vehicles {
		mustAddVehicle(t, s, vid, 3, int64(i*10), 0)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 100; i++ {
				now := rng.Int63n(10000)
				switch rng.Intn(4) {
				case 0:
					pickup := rng.Int63n(200)
					s.SubmitOrder(fmt.Sprintf("g%d-o%d", g, i), pickup, pickup+1+rng.Int63n(50),
						1+rng.Intn(2), rng.Int63n(30), now+rng.Int63n(300), now)
				case 1:
					s.UpdateVehiclePosition(vehicles[rng.Intn(3)], rng.Int63n(300), now)
				case 2:
					s.CancelOrder(fmt.Sprintf("g%d-o%d", rng.Intn(8), rng.Intn(100)), now)
				case 3:
					s.QueryOrder(fmt.Sprintf("g%d-o%d", rng.Intn(8), rng.Intn(100)))
				}
			}
		}(g)
	}
	wg.Wait()
	for _, vid := range vehicles {
		if err := s.checkVehicleInvariant(vid); err != nil {
			t.Fatalf("invariant: %v", err)
		}
	}
}
