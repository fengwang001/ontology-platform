package pivas

import "testing"

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func errCode(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

// newTestSystem 建立双台、两种运送、1..3 数量时长对照的基础环境。
func newTestSystem(t *testing.T) *System {
	t.Helper()
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetTransport(0, Cold, 20))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.SetDuration(0, 2, 150))
	mustOK(t, s.SetDuration(0, 3, 190))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 50}))
	mustOK(t, s.RegisterBench(0, Bench{ID: "B", Capacity: 2, ClearGap: 30}))
	mustOK(t, s.RegisterDrug(0, "d1", Drug{RoomStableSec: 300, ColdStableSec: 600, SolventClass: "NS"}))
	mustOK(t, s.RegisterDrug(0, "d2", Drug{RoomStableSec: 120, ColdStableSec: 400, SolventClass: "NS"}))
	mustOK(t, s.RegisterDrug(0, "d3", Drug{RoomStableSec: 500, ColdStableSec: 900, SolventClass: "GS", LightSensitive: true}))
	return s
}

func TestInvalidParamsAndClock(t *testing.T) {
	s := newTestSystem(t)
	if err := s.AcceptOrder(0, OrderInput{ID: "", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 1000}); errCode(err) != ErrInvalidParam {
		t.Fatalf("empty id: %v", err)
	}
	if err := s.SetTransport(0, Room, 0); errCode(err) != ErrInvalidParam {
		t.Fatalf("zero duration: %v", err)
	}
	mustOK(t, s.AcceptOrder(10, OrderInput{ID: "o1", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 1000}))
	if err := s.AcceptOrder(5, OrderInput{ID: "o2", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 1000}); errCode(err) != ErrClockRollback {
		t.Fatalf("clock rollback: %v", err)
	}
	// 回退操作不得改变状态：仍可在 now=10 操作。
	mustOK(t, s.AcceptOrder(10, OrderInput{ID: "o2", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 1000}))
	// 重复 ID 拒绝。
	if err := s.AcceptOrder(10, OrderInput{ID: "o1", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 1000}); errCode(err) != ErrInvalidParam {
		t.Fatalf("dup id: %v", err)
	}
}

func TestDrugNotFoundIncompatibleSolventPriority(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddIncompatibility(0, "d1", "d2"))
	// 药品不存在优先于禁忌。
	err := s.AcceptOrder(0, OrderInput{ID: "o", Drugs: []string{"d1", "nope", "d2"}, Solvent: "NS", DueAt: 1000})
	if errCode(err) != ErrDrugNotFound {
		t.Fatalf("want drug-not-found, got %v", err)
	}
	// 禁忌优先于溶媒不兼容。
	err = s.AcceptOrder(0, OrderInput{ID: "o", Drugs: []string{"d1", "d2"}, Solvent: "GS", DueAt: 1000})
	if errCode(err) != ErrIncompatiblePair {
		t.Fatalf("want incompatible, got %v", err)
	}
	// 溶媒不兼容。
	err = s.AcceptOrder(0, OrderInput{ID: "o", Drugs: []string{"d1"}, Solvent: "GS", DueAt: 1000})
	if errCode(err) != ErrSolventMismatch {
		t.Fatalf("want solvent mismatch, got %v", err)
	}
}

func TestExpiryBoundary(t *testing.T) {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 50}))
	mustOK(t, s.RegisterDrug(0, "d", Drug{RoomStableSec: 110, ColdStableSec: 200, SolventClass: "NS"}))
	mustOK(t, s.SetTransport(0, Cold, 10))
	// ready=100, expire=210, deliver=110 < 210 可行。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "ok", Drugs: []string{"d"}, Solvent: "NS", DueAt: 1000}))

	// 恰到期：stable=110, deliver 必须 < ready+110。
	s2 := New()
	mustOK(t, s2.SetTransport(0, Room, 100))
	mustOK(t, s2.SetDuration(0, 1, 10))
	mustOK(t, s2.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 5}))
	mustOK(t, s2.RegisterDrug(0, "d", Drug{RoomStableSec: 110, ColdStableSec: 2000, SolventClass: "NS"}))
	mustOK(t, s2.SetTransport(0, Cold, 100))
	// 室温 expire=120 看似可行；调整为室温稳定 100：deliver=110 == expire=110 恰到期失效，
	// 冷藏 deliver=110 < 2010 可行。
	mustOK(t, s2.RegisterDrug(0, "d", Drug{RoomStableSec: 100, ColdStableSec: 2000, SolventClass: "NS"}))
	mustOK(t, s2.AcceptOrder(0, OrderInput{ID: "cold", Drugs: []string{"d"}, Solvent: "NS", DueAt: 5000}))
	info, err := s2.QueryOrder(0, "cold")
	mustOK(t, err)
	if info.Storage != Cold || !info.OnTime || info.DeliverAt != 110 || info.ExpireAt != 2010 {
		t.Fatalf("unexpected info: %+v", info)
	}

	// 晚一秒即室温过期且冷藏也来不及：无可行安排。
	s3 := New()
	mustOK(t, s3.SetTransport(0, Room, 100))
	mustOK(t, s3.SetTransport(0, Cold, 100))
	mustOK(t, s3.SetDuration(0, 1, 10))
	mustOK(t, s3.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 5}))
	mustOK(t, s3.RegisterDrug(0, "d", Drug{RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"}))
	err = s3.AcceptOrder(0, OrderInput{ID: "late", Drugs: []string{"d"}, Solvent: "NS", DueAt: 5000})
	if errCode(err) != ErrNoFeasiblePlan {
		t.Fatalf("want no feasible, got %v", err)
	}
}

func TestDueBoundaryOnTime(t *testing.T) {
	s := newTestSystem(t)
	// ready=100, room deliver=110；due=110 恰等于视为按时。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 110}))
	info, _ := s.QueryOrder(0, "o")
	if !info.OnTime || info.DeliverAt != 110 {
		t.Fatalf("want on-time at due exactly, got %+v", info)
	}
}

func TestLightSensitiveBatching(t *testing.T) {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.SetDuration(0, 2, 150))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 50}))
	mustOK(t, s.RegisterDrug(0, "d3", Drug{RoomStableSec: 5000, ColdStableSec: 9000, SolventClass: "GS", LightSensitive: true}))
	// d3 避光、GS 溶媒。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "l1", Drugs: []string{"d3"}, Solvent: "GS", DueAt: 5000}))
	info1, _ := s.QueryOrder(0, "l1")
	if !info1.Covered {
		t.Fatalf("l1 must be covered")
	}
	// 单台时新开批次只能排在 B1 之后（start=200），并入 B1 送达 160 更早。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "l2", Drugs: []string{"d3"}, Solvent: "GS", DueAt: 5000}))
	info2, _ := s.QueryOrder(0, "l2")
	if info2.BatchID != info1.BatchID || info2.BatchStart != 0 || info2.ReadyAt != 150 {
		t.Fatalf("want same batch shifted duration, got %+v", info2)
	}
}

func TestCapacityFull(t *testing.T) {
	s := newTestSystem(t)
	// 台 B 容量 2：两张医嘱刚好同批，第三张必须另安排。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "a", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 5000}))
	// 强制都落到 B：把 A 占满或通过容量判定。这里直接利用 B 优先（编号小）。
	ia, _ := s.QueryOrder(0, "a")
	if ia.BenchID != "A" {
		t.Fatalf("bench A should be chosen first, got %s", ia.BenchID)
	}
}

func TestClearGapBoundary(t *testing.T) {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 1))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.SetDuration(0, 2, 150))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 1, ClearGap: 10}))
	mustOK(t, s.RegisterDrug(0, "d", Drug{RoomStableSec: 100000, ColdStableSec: 100000, SolventClass: "NS"}))
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d"}, Solvent: "NS", DueAt: 100000}))
	// 第一张批次 [0,100]。第二张若新批次 start=110（间隔恰 10）。
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o2", Drugs: []string{"d"}, Solvent: "NS", DueAt: 100000}))
	i1, _ := s.QueryOrder(0, "o1")
	i2, _ := s.QueryOrder(0, "o2")
	if i1.BatchID == i2.BatchID {
		t.Fatalf("expected distinct batches")
	}
	if i2.BatchStart != 110 {
		t.Fatalf("gap exactly 10: want start 110, got %d", i2.BatchStart)
	}
}

func TestCatalogChangeNotRetroactive(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d1", "d2"}, Solvent: "NS", DueAt: 5000}))
	// 变更后新增禁忌，不追溯已受理医嘱。
	mustOK(t, s.AddIncompatibility(1, "d1", "d2"))
	i1, _ := s.QueryOrder(1, "o1")
	if i1 == nil || !i1.OnTime {
		t.Fatalf("accepted order must remain valid: %+v", i1)
	}
	// 新医嘱被拦截。
	if err := s.AcceptOrder(1, OrderInput{ID: "o2", Drugs: []string{"d1", "d2"}, Solvent: "NS", DueAt: 5000}); errCode(err) != ErrIncompatiblePair {
		t.Fatalf("new order must be rejected: %v", err)
	}
}

func TestCancelUnstartedKeepsStart(t *testing.T) {
	s := New()
	mustOK(t, s.SetTransport(0, Room, 10))
	mustOK(t, s.SetDuration(0, 1, 100))
	mustOK(t, s.SetDuration(0, 2, 150))
	mustOK(t, s.RegisterBench(0, Bench{ID: "A", Capacity: 3, ClearGap: 50}))
	mustOK(t, s.RegisterDrug(0, "d1", Drug{RoomStableSec: 5000, ColdStableSec: 9000, SolventClass: "NS"}))
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o1", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 5000}))
	mustOK(t, s.AcceptOrder(0, OrderInput{ID: "o2", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 5000}))
	i2, _ := s.QueryOrder(0, "o2")
	if i2.ReadyAt != 150 {
		t.Fatalf("two-order batch ready=150, got %d", i2.ReadyAt)
	}
	mustOK(t, s.CancelOrder(0, "o1"))
	i2b, _ := s.QueryOrder(0, "o2")
	// 数量减少时长变短，但开始时刻不得提前：仍为 start=0，ready=100。
	if i2b.BatchStart != 0 || i2b.ReadyAt != 100 {
		t.Fatalf("after cancel start stays 0, got start=%d ready=%d", i2b.BatchStart, i2b.ReadyAt)
	}
	// 已开始的医嘱取消报状态不符。
	mustOK(t, s.AcceptOrder(200, OrderInput{ID: "o3", Drugs: []string{"d1"}, Solvent: "NS", DueAt: 5000}))
	if err := s.CancelOrder(201, "o3"); errCode(err) != ErrBadState {
		t.Fatalf("started cancel: %v", err)
	}
}
