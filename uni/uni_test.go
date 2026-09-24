package uni

import (
	"math/rand"
	"sync"
	"testing"
)

// TestRemoveCheckCountConstant：cnt[a]=m 时 Remove，判断「是否还被其它来源持有」
// 所检查的分区数不得随 m 线性增长；这里直接读非导出计数器（白盒，仅此包可读）。
func TestRemoveCheckCountConstant(t *testing.T) {
	const c = 2 // 与 m 无关的小常数上界
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		u := New(m)
		for p := 0; p < m; p++ {
			u.Add(p, "a")
		}
		if _, out := u.Remove(0, "a"); out || u.lastRemoveChecks > c {
			t.Fatalf("m=%d: first remove emitted=%v checks=%d", m, out, u.lastRemoveChecks)
		}
		var lastChecks int
		for p := 1; p < m; p++ {
			_, out := u.Remove(p, "a")
			lastChecks = u.lastRemoveChecks
			if last := p == m-1; last && (!out || lastChecks > c) {
				t.Fatalf("m=%d: last remove emitted=%v checks=%d", m, out, lastChecks)
			} else if !last && out {
				t.Fatalf("m=%d: middle remove unexpectedly emitted", m)
			}
		}
	}
}

// TestRemoveCheckCountNotLinear：跨档记录检查个数，断言它完全不随 m 增长（恒为常数）。
func TestRemoveCheckCountNotLinear(t *testing.T) {
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		u := New(m)
		for p := 0; p < m; p++ {
			u.Add(p, "a")
		}
		u.Remove(0, "a")
		if first == -1 {
			first = u.lastRemoveChecks
		} else if u.lastRemoveChecks != first {
			t.Fatalf("checks grew with m: m=%d got %d, baseline %d", m, u.lastRemoveChecks, first)
		}
	}
}

// TestRandomOrderVsBatch：随机操作顺序下，View 始终等于批量重算的去重并集，
// 且 changelog 可顺序复现视图（循环生成随机顺序）。
func TestRandomOrderVsBatch(t *testing.T) {
	const nPart = 6
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 50; iter++ {
		u := New(nPart)
		ref := make([]map[string]struct{}, nPart)
		for i := range ref {
			ref[i] = map[string]struct{}{}
		}
		for k := 0; k < 300; k++ {
			p := rng.Intn(nPart)
			e := string(rune('a' + rng.Intn(8)))
			if rng.Intn(2) == 0 {
				u.Add(p, e)
				ref[p][e] = struct{}{}
			} else {
				u.Remove(p, e)
				delete(ref[p], e)
			}
		}
		if err := u.Verify(); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
	}
}

// TestConcurrentWritersRace：Add/Remove/View/Changes 混打，-race 下无竞争，changelog 仍复现视图。
func TestConcurrentWritersRace(t *testing.T) {
	const nPart = 8
	u := New(nPart)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for k := 0; k < 200; k++ {
				p, e := rng.Intn(nPart), string(rune('a'+rng.Intn(8)))
				if rng.Intn(2) == 0 {
					u.Add(p, e)
				} else {
					u.Remove(p, e)
				}
				u.View()
			}
		}(g)
	}
	wg.Wait()
	if err := u.Verify(); err != nil {
		t.Fatal(err)
	}
}
