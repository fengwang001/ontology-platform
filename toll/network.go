package toll

import (
	"container/heap"
	"slices"
	"time"
)

// Segment is a directed road segment between two gantries.
type Segment struct {
	From  string
	To    string
	Miles int64
	Rates map[VehicleClass]Money // fee per mile per vehicle class
}

// Network is the directed gantry graph.
type Network struct {
	gantries map[string]bool
	seg      map[string]map[string]Segment // from -> to -> segment
}

func NewNetwork() *Network {
	return &Network{gantries: map[string]bool{}, seg: map[string]map[string]Segment{}}
}

func (n *Network) hasGantry(id string) bool { return n.gantries[id] }

func (n *Network) addGantry(id string) { n.gantries[id] = true }

func (n *Network) setSegment(s Segment) {
	if n.seg[s.From] == nil {
		n.seg[s.From] = map[string]Segment{}
	}
	n.seg[s.From][s.To] = s
}

// Waypoint is a gantry the vehicle is known to have passed, with the
// passing time.
type Waypoint struct {
	Gantry string
	TS     time.Time
}

// dijkstraItem is a priority-queue entry: the lexicographically smallest
// path reaching Node with Cost.
type dijkstraItem struct {
	node string
	cost int64
	path []string
}

type dijkstraHeap []dijkstraItem

func (h dijkstraHeap) Len() int { return len(h) }
func (h dijkstraHeap) Less(i, j int) bool {
	if h[i].cost != h[j].cost {
		return h[i].cost < h[j].cost
	}
	return slices.Compare(h[i].path, h[j].path) < 0
}
func (h dijkstraHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dijkstraHeap) Push(x any)        { *h = append(*h, x.(dijkstraItem)) }
func (h *dijkstraHeap) Pop() (x any) {
	old := *h
	x = old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// better reports whether (cost, path) improves on the current best.
func better(cost int64, path []string, bestCost int64, bestPath []string, seen bool) bool {
	if !seen || cost != bestCost {
		return !seen || cost < bestCost
	}
	return slices.Compare(path, bestPath) < 0
}

// shortestPaths runs Dijkstra from src; weight returns the edge weight and
// whether the edge is usable. It returns, per reachable node, the best cost
// and the lexicographically smallest minimum-cost gantry sequence.
func (n *Network) shortestPaths(src string, weight func(Segment) (int64, bool)) (map[string]int64, map[string][]string) {
	dist := map[string]int64{src: 0}
	paths := map[string][]string{src: {src}}
	pq := &dijkstraHeap{{node: src, cost: 0, path: []string{src}}}
	for pq.Len() > 0 {
		cur := heap.Pop(pq).(dijkstraItem)
		if cur.cost != dist[cur.node] || slices.Compare(cur.path, paths[cur.node]) != 0 {
			continue // stale entry
		}
		for to, seg := range n.seg[cur.node] {
			w, ok := weight(seg)
			if !ok {
				continue
			}
			nc := cur.cost + w
			np := append(slices.Clone(cur.path), to)
			if !better(nc, np, dist[to], paths[to], hasKey(dist, to)) {
				continue
			}
			dist[to] = nc
			paths[to] = np
			heap.Push(pq, dijkstraItem{node: to, cost: nc, path: np})
		}
	}
	return dist, paths
}

func hasKey(m map[string]int64, k string) bool { _, ok := m[k]; return ok }

// mileageDist returns minimum-mileage distances from src (used only to
// interpolate passing times at unrecorded gantries).
func (n *Network) mileageDist(src string) map[string]int64 {
	dist, _ := n.shortestPaths(src, func(s Segment) (int64, bool) { return s.Miles, true })
	return dist
}

// legPath finds the minimum-fee path for one leg from A to B, where the
// vehicle is known to be at A at tA and at B at tB. The passing time at an
// unrecorded gantry x is interpolated linearly by mileage:
//
//	t(x) = tA + (tB-tA) * dm(A,x)/dm(A,B)   (dm = min-mileage distance)
//
// Each segment is rated with the vehicle class effective at the
// interpolated passing time of its start gantry. Ties on total fee are
// broken by the lexicographically smallest gantry sequence.
func (n *Network) legPath(a, b string, tA, tB time.Time, classAt func(time.Time) VehicleClass) ([]string, Money, bool) {
	dm := n.mileageDist(a)
	dmB, ok := dm[b]
	if !ok {
		return nil, 0, false
	}
	span := tB.Sub(tA)
	timeAt := func(x string) time.Time {
		if dmB == 0 {
			return tA
		}
		frac := float64(dm[x]) / float64(dmB)
		return tA.Add(time.Duration(float64(span) * frac))
	}
	weight := func(s Segment) (int64, bool) {
		rate, ok := s.Rates[classAt(timeAt(s.From))]
		if !ok {
			return 0, false // segment not rated for this class: unusable
		}
		return s.Miles * int64(rate), true
	}
	dist, paths := n.shortestPaths(a, weight)
	p, ok := paths[b]
	if !ok {
		return nil, 0, false
	}
	return p, Money(dist[b]), true
}

// InferPath reconstructs the billing path through the given waypoints (in
// travel order): per leg the minimum-fee path, ties broken by the
// lexicographically smallest gantry sequence. Returns the full gantry
// sequence and the total fee.
func (n *Network) InferPath(wps []Waypoint, classAt func(time.Time) VehicleClass) ([]string, Money, bool) {
	if len(wps) < 2 {
		return nil, 0, false
	}
	full := []string{}
	var total Money
	for i := 1; i < len(wps); i++ {
		leg, fee, ok := n.legPath(wps[i-1].Gantry, wps[i].Gantry, wps[i-1].TS, wps[i].TS, classAt)
		if !ok {
			return nil, 0, false
		}
		full = append(full, leg[:len(leg)-1]...)
		total += fee
	}
	full = append(full, wps[len(wps)-1].Gantry)
	return full, total, true
}
