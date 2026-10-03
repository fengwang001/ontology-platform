package roadnet

import (
	"sync"
	"testing"
)

// The query must stop as soon as the goal is settled: on a grid with
// the goal next to the source, popped stays tiny compared to the node
// count. (The contract caps N at 5000, so the grid is 70x70=4900
// nodes rather than 300x300; the asymptotic point is identical.)
func TestPoppedStaysLocalOnGrid(t *testing.T) {
	const side = 70
	nt := newNet(t, side*side, 2*side*side)
	node := func(x, y int) int { return y*side + x }
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if x+1 < side {
				mustAdd(t, nt, node(x, y), node(x+1, y), []Segment{{0, 1}})
			}
			if y+1 < side {
				mustAdd(t, nt, node(x, y), node(x, y+1), []Segment{{0, 1}})
			}
		}
	}
	res := mustQuery(t, nt, node(0, 0), 0, node(3, 4), nil)
	if res.Arrival != 7 {
		t.Fatalf("arrival = %d, want 7", res.Arrival)
	}
	// Nodes with d <= 7 on the grid: 36. popped must stay in that
	// league, nowhere near 4900.
	if res.Popped > 40 {
		t.Fatalf("popped = %d, want <= 40", res.Popped)
	}
	t.Logf("网格 %dx%d=%d 节点, 目标邻近出发点: popped=%d (远小于节点总数)",
		side, side, side*side, res.Popped)
}

// All operations are safe for concurrent use; -race validates the
// locking. Queries must observe consistent snapshots (no half
// registered announcements).
func TestConcurrentUse(t *testing.T) {
	nt := newNet(t, 50, 4000)
	// A backbone so queries usually succeed.
	for i := 0; i < 49; i++ {
		mustAdd(t, nt, i, i+1, []Segment{{0, 1}})
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers: edges, announcements, clock advances.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				u := (w*7 + i*13) % 50
				v := (w*11 + i*17 + 1) % 50
				if u == v {
					v = (v + 1) % 50
				}
				nt.AddEdge(u, v, []Segment{{0, int64(1 + i%5)}, {int64(i % 7), int64(1 + i%3)}})
				id := 1 + (i*31+w)%nt.EdgeCount()
				eff := nt.Now() + int64(i%5)
				nt.Announce(id, eff, []Segment{{0, int64(1 + i%4)}})
				if i%10 == 0 {
					nt.Advance(nt.Now() + 1)
				}
			}
		}(w)
	}
	// Readers: concurrent queries must never fail spuriously or see
	// torn state.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				select {
				case <-stop:
					return
				default:
				}
				s := (w*3 + i) % 50
				g := (w*5 + i*7) % 50
				res, err := nt.EarliestArrival(s, int64(i%20), g, nil)
				if err != nil {
					continue // unreachable is fine
				}
				// Route sanity: arrivals chain monotonically.
				prev := int64(i % 20)
				for _, leg := range res.Legs {
					if leg.Depart < prev || leg.Arrive <= leg.Depart {
						t.Errorf("torn route: %+v", res)
						return
					}
					prev = leg.Arrive
				}
				if len(res.Legs) > 0 && res.Legs[len(res.Legs)-1].Arrive != res.Arrival {
					t.Errorf("arrival mismatch: %+v", res)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	t.Logf("并发完成: version=%d edges=%d now=%d", nt.Version(), nt.EdgeCount(), nt.Now())
}
