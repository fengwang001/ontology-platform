package toll

import "container/heap"

// Segment 是门架间的有向路段。
type Segment struct {
	From     string
	To       string
	Distance int64            // 里程(>0)
	Rates    map[string]int64 // 车型 -> 单位里程费率(>=0),缺失车型按 0 计
}

// Network 是有向路网。
type Network struct {
	gantries map[string]struct{}
	adj      map[string][]*Segment
}

func NewNetwork() *Network {
	return &Network{
		gantries: make(map[string]struct{}),
		adj:      make(map[string][]*Segment),
	}
}

func (n *Network) AddGantry(id string) error {
	if id == "" {
		return ErrInvalidParams
	}
	n.gantries[id] = struct{}{}
	return nil
}

func (n *Network) AddSegment(from, to string, distance int64, rates map[string]int64) error {
	if from == "" || to == "" || from == to || distance <= 0 {
		return ErrInvalidParams
	}
	for _, r := range rates {
		if r < 0 {
			return ErrInvalidParams
		}
	}
	n.gantries[from] = struct{}{}
	n.gantries[to] = struct{}{}
	cp := make(map[string]int64, len(rates))
	for k, v := range rates {
		cp[k] = v
	}
	n.adj[from] = append(n.adj[from], &Segment{From: from, To: to, Distance: distance, Rates: cp})
	return nil
}

func (n *Network) HasGantry(id string) bool {
	_, ok := n.gantries[id]
	return ok
}

// lessPath 按门架序列字典序比较(前缀则短者小)。
func lessPath(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func containsNode(path []string, node string) bool {
	for _, p := range path {
		if p == node {
			return true
		}
	}
	return false
}

type pathCandidate struct {
	node string
	cost int64
	path []string
}

type candidateHeap []pathCandidate

func (h candidateHeap) Len() int { return len(h) }
func (h candidateHeap) Less(i, j int) bool {
	if h[i].cost != h[j].cost {
		return h[i].cost < h[j].cost
	}
	return lessPath(h[i].path, h[j].path)
}
func (h candidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)   { *h = append(*h, x.(pathCandidate)) }
func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func betterCandidate(a, b pathCandidate) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return lessPath(a.path, b.path)
}

// shortestPath 在简单路径中求 from 到 to 的最小费用路径;
// 费用相同取门架序列字典序最小者。weight 给出单条路段的费用。
func (n *Network) shortestPath(from, to string, weight func(*Segment) int64) ([]string, int64, bool) {
	if from == to {
		return []string{from}, 0, true
	}
	best := make(map[string]pathCandidate)
	done := make(map[string]bool)
	h := &candidateHeap{{node: from, cost: 0, path: []string{from}}}
	for h.Len() > 0 {
		cur := heap.Pop(h).(pathCandidate)
		if done[cur.node] {
			continue
		}
		if b, ok := best[cur.node]; ok && betterCandidate(b, cur) {
			continue // 堆中过期项
		}
		done[cur.node] = true
		best[cur.node] = cur
		if cur.node == to {
			return cur.path, cur.cost, true
		}
		for _, seg := range n.adj[cur.node] {
			if done[seg.To] || containsNode(cur.path, seg.To) {
				continue // 只考虑简单路径
			}
			np := make([]string, len(cur.path)+1)
			copy(np, cur.path)
			np[len(cur.path)] = seg.To
			cand := pathCandidate{node: seg.To, cost: cur.cost + weight(seg), path: np}
			if b, ok := best[seg.To]; ok && !betterCandidate(cand, b) {
				continue
			}
			best[seg.To] = cand
			heap.Push(h, cand)
		}
	}
	return nil, 0, false
}
