package toll

import (
	"container/heap"
	"fmt"
	"sort"
)

// edge 路网中的一条有向路段。
type edge struct {
	to      GantryID
	mileage int64
	rates   map[VehicleClass]int64
}

// Network 门架路网：节点为门架，边为有向路段。
type Network struct {
	cfg      Config
	adj      map[GantryID][]edge
	gantries map[GantryID]bool
}

// NewNetwork 构建并校验路网。每个路段必须给出全部合法车型的非负费率。
func NewNetwork(cfg Config, segs []Segment) (*Network, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	n := &Network{
		cfg:      cfg,
		adj:      map[GantryID][]edge{},
		gantries: map[GantryID]bool{},
	}
	for _, s := range segs {
		if s.From == "" || s.To == "" || s.Mileage < 0 {
			return nil, fmt.Errorf("%w: 路段 %q->%q 里程非法", ErrInvalidParam, s.From, s.To)
		}
		rates := make(map[VehicleClass]int64, len(cfg.Classes))
		for _, c := range cfg.Classes {
			r, ok := s.Rates[c]
			if !ok || r < 0 {
				return nil, fmt.Errorf("%w: 路段 %q->%q 缺少车型 %q 的合法费率", ErrInvalidParam, s.From, s.To, c)
			}
			rates[c] = r
		}
		n.gantries[s.From] = true
		n.gantries[s.To] = true
		n.adj[s.From] = append(n.adj[s.From], edge{to: s.To, mileage: s.Mileage, rates: rates})
	}
	for g := range n.adj {
		sort.Slice(n.adj[g], func(i, j int) bool {
			if n.adj[g][i].to != n.adj[g][j].to {
				return n.adj[g][i].to < n.adj[g][j].to
			}
			return n.adj[g][i].mileage < n.adj[g][j].mileage
		})
	}
	return n, nil
}

func (n *Network) hasGantry(g GantryID) bool { return n.gantries[g] }

func feeOf(e edge, class VehicleClass) int64 { return e.mileage * e.rates[class] }

// lessPath 门架序列字典序比较；前缀序列视为更小。
func lessPath(a, b []GantryID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// pathItem 是优先队列元素：键为 (费用, 门架序列) 的字典序。
type pathItem struct {
	cost int64
	path []GantryID
	node GantryID
}

type pathPQ []pathItem

func (q pathPQ) Len() int { return len(q) }
func (q pathPQ) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return lessPath(q[i].path, q[j].path)
}
func (q pathPQ) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *pathPQ) Push(x interface{}) { *q = append(*q, x.(pathItem)) }
func (q *pathPQ) Pop() interface{} {
	old := *q
	it := old[len(old)-1]
	*q = old[:len(old)-1]
	return it
}

// shortest 返回 src 到 dst 在给定车型下的 (费用最低, 门架序列字典序最小) 路径。
// 费率为非负数，路径键随扩展单调不减，故首次弹出即最优（广义 Dijkstra）。
func (n *Network) shortest(src, dst GantryID, class VehicleClass) ([]GantryID, int64, bool) {
	if src == dst {
		return []GantryID{src}, 0, true
	}
	finalized := map[GantryID]bool{}
	pq := &pathPQ{{cost: 0, path: []GantryID{src}, node: src}}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pathItem)
		if finalized[it.node] {
			continue
		}
		finalized[it.node] = true
		if it.node == dst {
			return it.path, it.cost, true
		}
		for _, e := range n.adj[it.node] {
			if finalized[e.to] {
				continue
			}
			np := make([]GantryID, len(it.path)+1)
			copy(np, it.path)
			np[len(it.path)] = e.to
			heap.Push(pq, pathItem{cost: it.cost + feeOf(e, class), path: np, node: e.to})
		}
	}
	return nil, 0, false
}

// Infer 在路网中选取依次经过 waypoints 的路径：总费用最低者优先，
// 并列时取门架序列字典序最小者。classes[i] 为经过 waypoints[i] 时刻对应的车型，
// 每段行程腿（waypoints[i] -> waypoints[i+1]）上的所有路段均按该车型计费
// （未记录门架的经过时刻按其前最近已知时刻计）。
// 各行程腿相互独立，因此逐腿取 (费用, 序列) 最优再拼接即全局最优。
func (n *Network) Infer(waypoints []GantryID, classes []VehicleClass) ([]GantryID, int64, bool) {
	if len(waypoints) == 0 || len(classes) != len(waypoints) {
		return nil, 0, false
	}
	if len(waypoints) == 1 {
		return append([]GantryID(nil), waypoints...), 0, true
	}
	var full []GantryID
	total := int64(0)
	for i := 0; i+1 < len(waypoints); i++ {
		p, c, ok := n.shortest(waypoints[i], waypoints[i+1], classes[i])
		if !ok {
			return nil, 0, false
		}
		total += c
		if i == 0 {
			full = append(full, p...)
		} else {
			full = append(full, p[1:]...)
		}
	}
	return full, total, true
}
