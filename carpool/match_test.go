package carpool

import "testing"

func mustService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustAddVehicle(t *testing.T, s *Service, id string, seats int, pos, now int64) {
	t.Helper()
	if err := s.AddVehicle(id, seats, pos, now); err != nil {
		t.Fatalf("AddVehicle(%s): %v", id, err)
	}
}

func mustSubmit(t *testing.T, s *Service, id string, pickup, dropoff int64, persons int, maxDelay, latest, now int64) SubmitResult {
	t.Helper()
	res, err := s.SubmitOrder(id, pickup, dropoff, persons, maxDelay, latest, now)
	if err != nil {
		t.Fatalf("SubmitOrder(%s): %v", id, err)
	}
	return res
}

func mustQuery(t *testing.T, s *Service, id string) OrderView {
	t.Helper()
	view, err := s.QueryOrder(id)
	if err != nil {
		t.Fatalf("QueryOrder(%s): %v", id, err)
	}
	return view
}

func testCfg() Config {
	return Config{StopDuration: 10, TimePerDistance: 1, UnitPrice: 1, CancelFee: 5, MaxActiveOrders: 8}
}

// assigned 报告状态是否表示已并入车辆（已匹配或已上车）。
func assigned(st OrderStatus) bool {
	return st == StatusMatched || st == StatusOnboard
}

// 停靠延误恰等于最大停靠延误时仍可并入，再增加一次停靠则拒绝。
func TestStopDelayExactlyMax(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	if r := mustSubmit(t, s, "a", 0, 100, 1, 20, 1000, 1); !assigned(r.Status) {
		t.Fatalf("a: got %v", r.Status)
	}
	// b 在 (0,100) 内新增 30、40 两个停靠点，a 的延误恰为 2*10=20。
	if r := mustSubmit(t, s, "b", 30, 40, 1, 0, 1000, 2); !assigned(r.Status) {
		t.Fatalf("b: got %v, want Matched (delay exactly max)", r.Status)
	}
	// c 会再增两个停靠点，a 的延误变为 40 > 20，c 无车可用进入等待。
	if r := mustSubmit(t, s, "c", 50, 60, 1, 0, 1000, 3); r.Status != StatusWaiting {
		t.Fatalf("c: got %v, want Waiting", r.Status)
	}
}

// 新乘客上下车点与已有停靠点重合时不增加任何延误。
func TestCoincidingPointsNoExtraDelay(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "a", 0, 100, 1, 20, 1000, 1)
	mustSubmit(t, s, "b", 30, 70, 1, 0, 1000, 2) // a 延误 = 2*10 = 20
	// c 的上下车点与 b 完全重合，不产生新停靠点，a 的延误保持 20。
	if r := mustSubmit(t, s, "c", 30, 70, 1, 0, 1000, 3); !assigned(r.Status) {
		t.Fatalf("c: got %v, want Matched (coinciding stops)", r.Status)
	}
	// d 的下车点 71 是新停靠点，a 的延误变为 30 > 20，d 进入等待。
	if r := mustSubmit(t, s, "d", 30, 71, 1, 0, 1000, 4); r.Status != StatusWaiting {
		t.Fatalf("d: got %v, want Waiting", r.Status)
	}
}

// 预计上车时刻恰等于最晚上车时刻时可匹配，晚一个时刻则不可。
func TestEstPickupExactlyLatest(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 10, 0, 0)
	mustSubmit(t, s, "a", 10, 20, 1, 100, 10000, 0)
	// b 的预计上车时刻 = 0 + 100*1 + 2*10 = 120，恰等于最晚上车时刻。
	if r := mustSubmit(t, s, "b", 100, 200, 1, 1000, 120, 0); !assigned(r.Status) {
		t.Fatalf("b: got %v, want Matched (est pickup == latest)", r.Status)
	}
	// c 的最晚上车时刻为 119 < 120，进入等待。
	if r := mustSubmit(t, s, "c", 100, 200, 1, 1000, 119, 0); r.Status != StatusWaiting {
		t.Fatalf("c: got %v, want Waiting", r.Status)
	}
}

// 两辆车延误增量相同时按车辆标识字典序选车。
func TestTieBreakByVehicleID(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v-b", 5, 0, 0)
	mustAddVehicle(t, s, "v-a", 5, 0, 0)
	r := mustSubmit(t, s, "a", 0, 10, 1, 0, 100, 1)
	if r.VehicleID != "v-a" {
		t.Fatalf("matched to %s, want v-a (lexicographically smaller)", r.VehicleID)
	}
}

// 座位恰好坐满时可匹配，再上一人则拒绝。
func TestSeatsExactlyFull(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v", 2, 0, 0)
	mustSubmit(t, s, "a", 0, 10, 1, 0, 100, 1)
	if r := mustSubmit(t, s, "b", 0, 10, 1, 0, 100, 2); !assigned(r.Status) {
		t.Fatalf("b: got %v, want Matched (seats exactly full)", r.Status)
	}
	if r := mustSubmit(t, s, "c", 0, 10, 1, 0, 100, 3); r.Status != StatusWaiting {
		t.Fatalf("c: got %v, want Waiting (would overload)", r.Status)
	}
}

// 延误增量最小者优先：并入使已有乘客延误增加更少的车被选中。
func TestMinDelayDeltaWins(t *testing.T) {
	s := mustService(t, testCfg())
	mustAddVehicle(t, s, "v1", 5, 0, 0)
	mustAddVehicle(t, s, "v2", 5, 0, 0)
	// v1 上已有乘客 a（0->100），v2 空车。
	mustSubmit(t, s, "a", 0, 100, 1, 1000, 10000, 1)
	// b 并入 v1 会在 a 的区间内新增两个停靠点（增量 20），并入 v2 增量为 0。
	r := mustSubmit(t, s, "b", 30, 40, 1, 0, 10000, 2)
	if r.VehicleID != "v2" {
		t.Fatalf("matched to %s, want v2 (smaller delay delta)", r.VehicleID)
	}
}
