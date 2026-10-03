package activator

import (
	"fmt"
	"sync"
	"testing"

	"ontology/config"
)

// TestReadyCascadeBound：Ready 级联检查的路由数不得超过
// “引用该集群的待激活路由数 + 1”，用非导出计数器 lastCascadeChecks 证明。
// 另放置同数量的不引用该集群的待激活路由，确保没有全表扫描。
func TestReadyCascadeBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("routes=%d", n), func(t *testing.T) {
			// 构造：n 条引用 ctr2 与 gate2；Ready gate2 后，ctr2 仍在预热中，
			// 所有 hit 路由保持待激活；再 Ready ctr2 时只允许检查这 n 条（+1 余量）。
			a := New(1_000_000_000)
			mustPush(t, a, 1, mkPush([]string{"ctr2", "other2", "gate2"}, nil, nil, nil), 0)
			mustReady(t, a, "ctr2", 0)
			mustReady(t, a, "other2", 0)
			mustPush(t, a, 2, mkPush([]string{"ctr2", "gate2"}, nil, nil, nil), 1)
			ups2 := make([]config.Route, 0, 2*n)
			for i := 0; i < n; i++ {
				ups2 = append(ups2, r1(fmt.Sprintf("hit%d", i), "ctr2", "gate2"))
			}
			for i := 0; i < n; i++ {
				ups2 = append(ups2, r1(fmt.Sprintf("miss%d", i), "other2", "gate2"))
			}
			mustPush(t, a, 3, mkPush(nil, ups2, nil, nil), 2)
			mustReady(t, a, "gate2", 3)

			// Ready ctr2：引用它的待激活路由恰为 n 条。
			if err := a.Ready("ctr2", 4); err != nil {
				t.Fatalf("Ready ctr2: %v", err)
			}
			checks := a.lastChecks()
			if checks > n+1 {
				t.Fatalf("n=%d cascade checks %d exceed bound %d", n, checks, n+1)
			}
			if checks != n {
				t.Fatalf("n=%d expected exactly %d candidate checks, got %d", n, n, checks)
			}
			// Ready 一个无人引用的集群：检查次数必须为 0，而不是 2n。
			mustPush(t, a, 4, mkPush([]string{"lonely"}, nil, nil, nil), 5)
			if err := a.Ready("lonely", 6); err != nil {
				t.Fatalf("Ready lonely: %v", err)
			}
			if got := a.lastChecks(); got != 0 {
				t.Fatalf("n=%d lonely cluster checks=%d, want 0", n, got)
			}
		})
	}
}

// TestConcurrentSerializability：高并发混合调用不崩、不竞态，
// 且所有在役路由引用的集群恒存在。
func TestConcurrentSerializability(t *testing.T) {
	a := New(1000)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	worker := func(seed int) {
		defer wg.Done()
		ver := int64(1)
		now := int64(0)
		for i := 0; i < 200; i++ {
			select {
			case <-stop:
				return
			default:
			}
			now += int64((i + seed) % 5)
			c := fmt.Sprintf("c%d", (i+seed)%6)
			switch (i + seed) % 4 {
			case 0:
				err := a.Push(ver, mkPush([]string{c}, []config.Route{r1(fmt.Sprintf("r%d", (i+seed)%6), c)}, nil, nil), now)
				if err == nil {
					ver++
				}
			case 1:
				_ = a.Ready(c, now)
			case 2:
				_, _, _ = a.Serving(fmt.Sprintf("r%d", (i+seed)%6), now)
			case 3:
				_, _ = a.State(c, now)
			}
		}
	}

	for w := 0; w < 12; w++ {
		wg.Add(1)
		go worker(w)
	}
	wg.Wait()
	close(stop)

	// 不变量：每条在役路由引用的集群都存在。
	a.mu.Lock()
	defer a.mu.Unlock()
	for rn, rt := range a.routes {
		if !rt.hasServing {
			continue
		}
		for _, c := range rt.servingRefs {
			if _, ok := a.clusters[c]; !ok {
				t.Fatalf("serving route %s references missing cluster %s", rn, c)
			}
		}
	}
	// 计数器与待激活引用一致。
	manual := map[string]int{}
	for _, rt := range a.routes {
		if rt.hasPending {
			for _, c := range rt.pendingRefs {
				manual[c]++
			}
		}
	}
	for c, k := range manual {
		if a.pendRefCount[c] != k {
			t.Fatalf("pendRefCount[%s]=%d want %d", c, a.pendRefCount[c], k)
		}
	}
	for c, k := range a.pendRefCount {
		if manual[c] != k {
			t.Fatalf("pendRefCount[%s]=%d not in manual", c, k)
		}
	}
}
