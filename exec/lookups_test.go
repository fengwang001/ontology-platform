package exec

import (
	"fmt"
	"testing"

	"ontology/action"
)

// TestLookupsIndependentOfInFlightCount 证明一次 Execute 为定位缓存项与
// 在途操作所做查找不超过 2 次，且与在途操作总数（100 vs 10000）无关。
func TestLookupsIndependentOfInFlightCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("inflight=%d", n), func(t *testing.T) {
			s := New(3)
			p := plat(kv("os", "linux"))
			for i := 0; i < n; i++ {
				d := fmt.Sprintf("digest-%05d", i)
				id, err := s.Execute(d, p, 1, true)
				if err != nil || id != i+1 {
					t.Fatalf("seed %d: id=%d err=%v", i, id, err)
				}
				s.registry.Lookups() // 清零播种期间计数
			}

			// 未命中缓存、未命中在途：各查一次，合计 2。
			s.registry.Lookups()
			id, err := s.Execute("brand-new-digest", p, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			stats := s.registry.Lookups()
			t.Logf("在途=%d Execute(新摘要,skipCache=false) 等待者=%d 查找: cache=%d inflight=%d (合计=%d) | 判定: 与在途数无关，上限 2",
				n, id, stats.Cache, stats.InFlight, stats.Cache+stats.InFlight)
			if stats.Cache != 1 || stats.InFlight != 1 {
				t.Fatalf("n=%d: got cache=%d inflight=%d, want 1/1", n, stats.Cache, stats.InFlight)
			}

			// skipCache=true：只查在途，合计 1。
			id, err = s.Execute("another-new-digest", p, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			stats = s.registry.Lookups()
			t.Logf("在途=%d Execute(新摘要,skipCache=true) 等待者=%d 查找: cache=%d inflight=%d (合计=%d) | 判定: 跳过缓存，只查在途",
				n, id, stats.Cache, stats.InFlight, stats.Cache+stats.InFlight)
			if stats.Cache != 0 || stats.InFlight != 1 {
				t.Fatalf("n=%d: got cache=%d inflight=%d, want 0/1", n, stats.Cache, stats.InFlight)
			}

			// 命中已有在途操作（附着）：缓存未命中 1 次 + 在途命中 1 次。
			id, err = s.Execute("digest-00000", p, 2, false)
			if err != nil {
				t.Fatal(err)
			}
			stats = s.registry.Lookups()
			t.Logf("在途=%d Execute(附着已有在途) 等待者=%d 查找: cache=%d inflight=%d (合计=%d) | 判定: 附着路径同样 ≤2，且优先级重算只遍历该操作等待者",
				n, id, stats.Cache, stats.InFlight, stats.Cache+stats.InFlight)
			if stats.Cache != 1 || stats.InFlight != 1 {
				t.Fatalf("n=%d attach: got cache=%d inflight=%d", n, stats.Cache, stats.InFlight)
			}

			// 缓存命中：只查缓存，合计 1，且不建操作。
			s.mu.Lock()
			s.cache.Put("cached-digest", 0)
			before := len(s.registry.All())
			s.mu.Unlock()
			id, err = s.Execute("cached-digest", p, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			stats = s.registry.Lookups()
			s.mu.Lock()
			after := len(s.registry.All())
			s.mu.Unlock()
			t.Logf("在途=%d Execute(缓存命中) 等待者=%d 查找: cache=%d inflight=%d | 判定: 立即 Cached，不建操作（在途 %d->%d）",
				n, id, stats.Cache, stats.InFlight, before, after)
			if stats.Cache != 1 || stats.InFlight != 0 || before != after {
				t.Fatalf("n=%d cached: cache=%d inflight=%d ops %d->%d", n, stats.Cache, stats.InFlight, before, after)
			}
		})
	}
}

// TestEffectivePrioOnlyTouchesOwnWaiters 重算有效优先级只访问该操作自己的等待者：
// 直接构造一个挂 10000 个等待者之外还有大量其它操作的环境，并验证优先级正确。
func TestEffectivePrioOnlyTouchesOwnWaiters(t *testing.T) {
	o := action.NewOp("d", action.Platform{{Key: "os", Value: "linux"}}, 1, action.NewWaiter(1, 3))
	o.Waiters[2] = action.NewWaiter(2, 7)
	o.Waiters[3] = action.NewWaiter(3, 2)
	if got := o.EffectivePrio(); got != 7 {
		t.Fatalf("effective prio = %d, want 7", got)
	}
	delete(o.Waiters, 2)
	if got := o.EffectivePrio(); got != 3 {
		t.Fatalf("effective prio after cancel = %d, want 3", got)
	}
	if got := (&action.Op{Waiters: map[int]*action.Waiter{}}).EffectivePrio(); got != -1 {
		t.Fatalf("empty op prio = %d, want -1", got)
	}
}
