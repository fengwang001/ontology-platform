package timetable

import (
	"sync"
	"testing"
)

// TestGridPopped 在 300x300 网格上验证：目标邻近出发点时，
// popped 远小于 90000，且不超过 d(x)<=d(g) 的节点数。
func TestGridPopped(t *testing.T) {
	const W = 300
	const H = 300
	N := W * H
	nw := newUnlimited(N, 2*N)
	p := []Segment{{0, 1}}
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			id := y*W + x
			if x+1 < W {
				if _, err := nw.AddEdge(id, id+1, p); err != nil {
					t.Fatal(err)
				}
			}
			if y+1 < H {
				if _, err := nw.AddEdge(id, id+W, p); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	s := 0
	// 目标就在出发点附近（曼哈顿距离 10）。
	g := 5*W + 5
	r, err := nw.EarliestArrival(s, 0, g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Arrival != 10 {
		t.Fatalf("arr=%d want 10", r.Arrival)
	}
	if r.Popped >= N {
		t.Fatalf("popped=%d must be < total nodes %d", r.Popped, N)
	}
	// d(x)=曼哈顿距离；d(x)<=10 的节点数为 (11*12)/2=66。
	wantBound := 0
	for d := 0; d <= 10; d++ {
		for x := 0; x <= d; x++ {
			y := d - x
			if x < W && y < H {
				wantBound++
			}
		}
	}
	if r.Popped > wantBound {
		t.Fatalf("popped=%d exceeds #nodes with d(x)<=d(g)=%d", r.Popped, wantBound)
	}
	t.Logf("grid: N=%d popped=%d (bound %d), route len=%d", N, r.Popped, wantBound, len(r.Route))

	// 远距离目标也只弹出 d<=d(g) 的节点（三角区域，随距离平方增长，
	// 而非遍历全部 90000）。
	g2 := 50*W + 50
	r2, err := nw.EarliestArrival(0, 0, g2)
	if err != nil || r2.Arrival != 100 {
		t.Fatalf("far: %v %v", r2, err)
	}
	// 独立求全路网标签，统计 d(x)<=d(g) 的节点数，作为上界核对。
	full := nw.fullDistances(0, 0, nw.Version())
	bound := 0
	for _, d := range full {
		if d >= 0 && d <= r2.Arrival {
			bound++
		}
	}
	if r2.Popped > bound {
		t.Fatalf("far popped=%d exceeds #nodes with d(x)<=d(g)=%d (%d)", r2.Popped, r2.Arrival, bound)
	}
	t.Logf("grid far: popped=%d bound=%d / %d", r2.Popped, bound, N)
}

// TestConcurrency 并发调用变更与查询，配合 -race 检查数据竞争，
// 并验证历史版本结果在操作前后逐字不变。
func TestConcurrency(t *testing.T) {
	nw, _ := New(10, 200)
	id, _ := nw.AddEdge(0, 1, []Segment{{0, 5}})
	base, err := nw.EarliestArrival(0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				switch k % 4 {
				case 0:
					_, _ = nw.AddEdge(k%10, (k+1)%10, []Segment{{0, int64(1 + k%5)}})
				case 1:
					_ = nw.Announce(id, int64(k), []Segment{{0, int64(1 + k%5)}})
				case 2:
					_ = nw.Advance(int64(k))
				case 3:
					r, qerr := nw.EarliestArrival(0, 0, 1)
					_ = r
					_ = qerr
				}
			}
		}(w)
	}
	wg.Wait()

	r, err := nw.EarliestArrival(0, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Arrival != base.Arrival || len(r.Route) != len(base.Route) {
		t.Fatalf("historical version result changed: %v vs %v", r, base)
	}
}
