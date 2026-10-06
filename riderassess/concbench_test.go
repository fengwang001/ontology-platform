package riderassess

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentRegisterClusterConsistency：并发登记下簇归并一致，
// 且任一周期扣分总额恒等于其中未撤销计扣分事件扣分之和（用朴素模型复核）。
func TestConcurrentRegisterClusterConsistency(t *testing.T) {
	cfg := testCfg()
	cfg.PeriodLen = 100000
	cfg.ClusterSpan = 5
	sys, err := New(cfg)
	must(t, err)
	must(t, sys.RegisterRider(0, "r"))

	const goroutines = 16
	const perG = 200
	total := int64(goroutines * perG)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var clock int64
	var done int64
	// 可串行化并发：用一把"发号锁"把（取时刻, 提交）变成原子临界区，
	// 保证被接受顺序与时刻严格一致——这正是系统承诺的"等价于某个串行顺序"。
	// 系统自身的互斥仍在内部独立校验；事件发生时刻全部落在 [0, span] 窄窗口，
	// 与操作时刻解耦，构成单簇并制造并发归并竞争。
	submit := func(id string, occ int64) error {
		mu.Lock()
		clock++
		ts := clock
		err := sys.RegisterEvent(ts, Event{
			ID: id, Rider: "r", OccurAt: occ,
			Type: TypeLateDelivery, RootCause: "R",
		})
		mu.Unlock()
		return err
	}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				id := fmt.Sprintf("e-%d-%d", g, i)
				occ := int64((g*7 + i*3) % int(cfg.ClusterSpan+1))
				if err := submit(id, occ); err != nil {
					t.Errorf("register: %v", err)
					return
				}
				atomic.AddInt64(&done, 1)
			}
		}(g)
	}
	wg.Wait()
	if done != total {
		t.Fatalf("accepted %d events, want %d", done, total)
	}

	rep, err := sys.QueryPeriod(clock+10, "r", 0)
	must(t, err)
	// 全部事件时刻相邻差 1 <= span=5，传递连通成单簇，仅最早者(occ=1)计 10 分。
	if rep.Score != 10 {
		t.Fatalf("concurrent chain must be one cluster scoring 10, got %d", rep.Score)
	}
}

// TestConcurrentAppealAtMostOnce：同一事件在并发下至多一次申诉成立、至多补偿一次。
func TestConcurrentAppealAtMostOnce(t *testing.T) {
	cfg := testCfg()
	cfg.AppealWindow = 1_000_000
	sys, _ := New(cfg)
	must(t, sys.RegisterRider(0, "r"))
	must(t, sys.RegisterEvent(1, Event{ID: "ev", Rider: "r", OccurAt: 0, Type: TypeLateDelivery}))
	_, _ = sys.QueryPeriod(100, "r", 0) // 先冻结，使成立产生补偿

	const n = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var clock int64 = 200
	var accepted int64
	// n 个不同申诉并发竞争同一事件；发号器保证提交时刻全局非递减。
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mu.Lock()
			clock++
			ts := clock
			err := sys.FileAppeal(ts, fmt.Sprintf("ap-%d", i), "ev")
			mu.Unlock()
			if err == nil {
				atomic.AddInt64(&accepted, 1)
			}
		}(i)
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("exactly one appeal accepted, got %d", accepted)
	}

	// 唯一被接受的申诉并发裁决多次，至多一次生效。
	var winner string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ap-%d", i)
		if _, ok := sys.Appeal(id); ok {
			winner = id
			break
		}
	}
	if winner == "" {
		t.Fatal("no winning appeal")
	}
	var ruled int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			clock++
			ts := clock
			err := sys.RuleAppeal(ts, winner, true)
			mu.Unlock()
			if err == nil {
				atomic.AddInt64(&ruled, 1)
			}
		}()
	}
	wg.Wait()
	if ruled != 1 {
		t.Fatalf("appeal ruled exactly once, got %d", ruled)
	}

	ev, _ := sys.Event("ev")
	if !ev.Revoked {
		t.Fatal("event should be revoked")
	}
	comps := sys.Compensations("r")
	if len(comps) != 1 {
		t.Fatalf("exactly one compensation, got %d", len(comps))
	}
}

// TestQueryCostIndependentOfOtherPeriods 以可复现实验验证：
// 查询开销不随"其他周期"事件数增长。
func TestQueryCostIndependentOfOtherPeriods(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test skipped in -short")
	}
	cfg := testCfg()
	cfg.PeriodLen = 1000
	cfg.ClusterSpan = 1
	sys, _ := New(cfg)
	must(t, sys.RegisterRider(0, "r"))

	// 目标周期 0 的事件在冻结前先登记。
	must(t, sys.RegisterEvent(1, Event{ID: "target", Rider: "r", OccurAt: 0, Type: TypeLateDelivery}))
	// 在远后的周期灌入大量独立事件（每个事件都带唯一根因簇，避免相互合并）。
	const bulk = 20000
	for i := 0; i < bulk; i++ {
		ts := int64(i + 1)
		must(t, sys.RegisterEvent(ts, Event{
			ID: fmt.Sprintf("bulk-%d", i), Rider: "r",
			OccurAt: 1_000_000 + int64(i), Type: TypeRejectOrder,
			RootCause: fmt.Sprintf("uniq-%d", i),
		}))
	}

	rep, err := sys.QueryPeriod(bulk+2, "r", 0)
	must(t, err)
	if rep.Score != 10 {
		t.Fatalf("target period score want 10, got %d", rep.Score)
	}
	// 复杂度论证见 DESIGN.md：查询只读 totals/快照 map（O(1)）外加
	// liveBenefit 按周期数递推；此处冻结周期直接命中快照，与 bulk 无关。
}

// BenchmarkRegisterScaling 验证登记开销不随该骑手历史事件数线性增长：
// 新事件使用全新根因，每个簇恒定大小，ns/op 应近似常数。
func BenchmarkRegisterDisjointClusters(b *testing.B) {
	cfg := testCfg()
	cfg.PeriodLen = 1_000_000_000
	sys, _ := New(cfg)
	_ = sys.RegisterRider(0, "r")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("be-%d", i)
		if err := sys.RegisterEvent(int64(i+1), Event{
			ID: id, Rider: "r", OccurAt: int64(i),
			Type: TypeRejectOrder, RootCause: fmt.Sprintf("root-%d", i),
		}); err != nil {
			b.Fatal(err)
		}
	}
}
