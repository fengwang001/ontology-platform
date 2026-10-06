package surge

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentDispatch 并发派单不突破持单上限：
// 每个骑手同时面对多于 MaxHeld 的待派订单，派单成功数不得超过上限。
func TestConcurrentDispatch(t *testing.T) {
	cfg := testCfg() // MaxHeld=2
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, s.AddRegion("A"), "region")
	const riders = 8
	for i := 0; i < riders; i++ {
		mustOK(t, s.RiderOnline(0, RiderID(fmt.Sprintf("K%d", i)), "A"), "online")
	}
	// 每位骑手竞争 5 张订单（>MaxHeld=2）。
	type attempt struct {
		order OrderID
		rider RiderID
		ok    bool
	}
	attempts := []attempt{}
	oid := 0
	for i := 0; i < riders; i++ {
		for j := 0; j < 5; j++ {
			id := OrderID(fmt.Sprintf("O%d", oid))
			oid++
			mustOK(t, s.CreateOrder(0, id, "A"), "create")
			attempts = append(attempts, attempt{order: id, rider: RiderID(fmt.Sprintf("K%d", i))})
		}
	}

	results := make([]bool, len(attempts))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range attempts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			a := attempts[idx]
			results[idx] = s.DispatchOrder(1, a.order, a.rider) == nil
		}(i)
	}
	close(start)
	wg.Wait()

	snap := s.Snapshot()
	for i := 0; i < riders; i++ {
		id := RiderID(fmt.Sprintf("K%d", i))
		if snap.HoldCount[id] > cfg.MaxHeld {
			t.Fatalf("rider %s held %d > cap %d", id, snap.HoldCount[id], cfg.MaxHeld)
		}
		if snap.HoldCount[id] != cfg.MaxHeld {
			t.Fatalf("rider %s held %d, want %d", id, snap.HoldCount[id], cfg.MaxHeld)
		}
	}
	// 恒等式：满持单骑手不计运力，运力应为 0。
	if c := snap.Regions["A"].Capacity; c != 0 {
		t.Fatalf("all riders full: capacity want 0, got %d", c)
	}
	// 待派数 = 创建数 - 派出数。
	dispatched := riders * cfg.MaxHeld
	if p := snap.Regions["A"].Pending; p != riders*5-dispatched {
		t.Fatalf("pending want %d, got %d", riders*5-dispatched, p)
	}
	// 每张订单最多派给一名骑手。
	for _, od := range snap.Orders {
		if od.Stage == 'D' && od.Rider == "" {
			t.Fatalf("dispatched order without rider: %+v", od)
		}
	}
}

// TestConcurrentMixed 混合操作并发：恒等式在任何交错后成立。
func TestConcurrentMixed(t *testing.T) {
	cfg := testCfg()
	s, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, s.AddRegion("A"), "A")
	mustOK(t, s.AddRegion("B"), "B")

	var wg sync.WaitGroup
	start := make(chan struct{})
	next := int64(100)
	var tmu sync.Mutex
	clock := func() TimeSec {
		tmu.Lock()
		defer tmu.Unlock()
		v := next
		next++
		return TimeSec(v)
	}

	worker := func(n int, body func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < n; i++ {
				body()
			}
		}()
	}
	// 上线/下线/移动。
	worker(50, func() {
		id := RiderID(fmt.Sprintf("R%d", int(clock())%20))
		_ = s.RiderOnline(clock(), id, "A")
	})
	worker(50, func() {
		id := RiderID(fmt.Sprintf("R%d", int(clock())%20))
		_ = s.RiderMove(clock(), id, "B")
	})
	worker(50, func() {
		id := RiderID(fmt.Sprintf("R%d", int(clock())%20))
		_ = s.RiderOffline(clock(), id)
	})
	// 创建/派单/完成/取消/评估交错。
	worker(80, func() {
		_ = s.CreateOrder(clock(), OrderID(fmt.Sprintf("O%d", int(clock())%120)), "A")
	})
	worker(80, func() {
		o := OrderID(fmt.Sprintf("O%d", int(clock())%120))
		r := RiderID(fmt.Sprintf("R%d", int(clock())%20))
		_ = s.DispatchOrder(clock(), o, r)
	})
	worker(60, func() {
		_, _ = s.CompleteOrder(clock(), OrderID(fmt.Sprintf("O%d", int(clock())%120)))
	})
	worker(30, func() {
		_, _ = s.Evaluate(clock(), "A")
	})
	close(start)
	wg.Wait()

	snap := s.Snapshot()
	assertSnapshotInvariants(t, snap, cfg)
}

func assertSnapshotInvariants(t *testing.T, snap Snapshot, cfg Config) {
	t.Helper()
	wantCap := map[RegionID]int{}
	held := map[RiderID]int{}
	for id, rd := range snap.Riders {
		if rd.Held > cfg.MaxHeld {
			t.Fatalf("rider %s held %d exceeds %d", id, rd.Held, cfg.MaxHeld)
		}
		if rd.Online && rd.Held < cfg.MaxHeld {
			wantCap[rd.Region]++
		}
		held[id] = rd.Held
	}
	for reg, rs := range snap.Regions {
		if rs.Capacity != wantCap[reg] {
			t.Fatalf("region %s capacity %d != %d", reg, rs.Capacity, wantCap[reg])
		}
	}
	wantPending := map[RegionID]int{}
	for _, od := range snap.Orders {
		if od.Stage == 'P' {
			wantPending[od.Region]++
		}
	}
	for reg, rs := range snap.Regions {
		if rs.Pending != wantPending[reg] {
			t.Fatalf("region %s pending %d != %d", reg, rs.Pending, wantPending[reg])
		}
	}
}

// BenchmarkEvaluateScales 评估耗时不随区域骑手/订单数增长。
func BenchmarkEvaluateScales(b *testing.B) {
	for _, n := range []int{1, 100, 10000} {
		b.Run(fmt.Sprintf("riders=%d", n), func(b *testing.B) {
			cfg := testCfg()
			s, _ := NewSystem(cfg)
			_ = s.AddRegion("A")
			for i := 0; i < n; i++ {
				_ = s.RiderOnline(0, RiderID(fmt.Sprintf("K%d", i)), "A")
				_ = s.CreateOrder(0, OrderID(fmt.Sprintf("O%d", i)), "A")
			}
			t := TimeSec(1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				t += 10
				if _, err := s.Evaluate(t, "A"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDispatchScales 派单/完成耗时不随平台订单总量增长。
func BenchmarkDispatchScales(b *testing.B) {
	for _, total := range []int{100, 100000} {
		b.Run(fmt.Sprintf("platform_orders=%d", total), func(b *testing.B) {
			cfg := testCfg()
			s, _ := NewSystem(cfg)
			_ = s.AddRegion("A")
			_ = s.RiderOnline(0, "K0", "A")
			for i := 0; i < total; i++ {
				_ = s.CreateOrder(0, OrderID(fmt.Sprintf("P%d", i)), "A")
			}
			t := TimeSec(1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				t += 2
				oid := OrderID(fmt.Sprintf("hot%d", i))
				if err := s.CreateOrder(t, oid, "A"); err != nil {
					b.Fatal(err)
				}
				if err := s.DispatchOrder(t+1, oid, "K0"); err != nil {
					b.Fatal(err)
				}
				if _, err := s.CompleteOrder(t+2, oid); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
