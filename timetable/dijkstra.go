package timetable

import "sort"

// heap 是按下界 dist 维护的最小二叉堆，存放节点编号。
type heap struct {
	nodes []int
	dist  []int64
}

func (h *heap) push(x int) {
	h.nodes = append(h.nodes, x)
	i := len(h.nodes) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.dist[h.nodes[p]] <= h.dist[h.nodes[i]] {
			break
		}
		h.nodes[p], h.nodes[i] = h.nodes[i], h.nodes[p]
		i = p
	}
}

func (h *heap) pop() int {
	ns := h.nodes
	top := ns[0]
	last := len(ns) - 1
	ns[0] = ns[last]
	h.nodes = ns[:last]
	i := 0
	for {
		l := 2*i + 1
		if l >= len(h.nodes) {
			break
		}
		small := l
		if r := l + 1; r < len(h.nodes) && h.dist[h.nodes[r]] < h.dist[h.nodes[l]] {
			small = r
		}
		if h.dist[h.nodes[i]] <= h.dist[h.nodes[small]] {
			break
		}
		h.nodes[i], h.nodes[small] = h.nodes[small], h.nodes[i]
		i = small
	}
	return top
}

// pathKey 是长度相同的候选紧边路径在分层 trie 上的节点：父秩 + 末边。
type pathKey struct {
	rank int
	edge int
}

func lessKey(a, b pathKey) bool {
	return a.rank < b.rank || (a.rank == b.rank && a.edge < b.edge)
}

// EarliestArrival 在 ver（缺省为当前版本）下求从 s 于 t0 出发到 g 的
// 最早到达时刻，并按紧边规则给出确定路线与各段出发时刻。
func (nw *Network) EarliestArrival(s int, t0 int64, g int, ver ...int) (*Result, error) {
	if s < 0 || g < 0 || t0 < 0 || t0 > maxTime {
		return nil, errorf(ErrInvalidArgument, "timetable: invalid query s=%d g=%d t0=%d", s, g, t0)
	}
	nw.mu.RLock()
	if s >= nw.n || g >= nw.n {
		nw.mu.RUnlock()
		return nil, errorf(ErrInvalidArgument, "timetable: query node out of range (N=%d)", nw.n)
	}
	v := nw.version
	if len(ver) > 1 {
		nw.mu.RUnlock()
		return nil, errorf(ErrInvalidArgument, "timetable: at most one version argument allowed")
	}
	if len(ver) == 1 {
		v = ver[0]
	}
	if v < 0 {
		nw.mu.RUnlock()
		return nil, errorf(ErrInvalidArgument, "timetable: version %d must be non-negative", v)
	}
	if v > nw.version {
		nw.mu.RUnlock()
		return nil, errorf(ErrVersionNotYetCreated, "timetable: version %d not yet created (current %d)", v, nw.version)
	}
	edges := nw.snapshot(v)
	n := nw.n
	nw.mu.RUnlock()

	adj := make([][]*cedge, n)
	for _, ce := range edges {
		adj[ce.u] = append(adj[ce.u], ce)
	}

	dist := make([]int64, n)
	settled := make([]bool, n)
	for i := range dist {
		dist[i] = inf
	}
	dist[s] = t0
	h := &heap{dist: dist, nodes: make([]int, 0, 64)}
	h.push(s)

	// poppedOrder 记录节点被最终确定的顺序；紧边 DAG 的前驱必早于
	// 后继被弹出，正序即可做层数递增的路径 DP。
	poppedOrder := make([]int, 0, 64)
	for len(h.nodes) > 0 {
		x := h.pop()
		if settled[x] {
			continue
		}
		// 目标已有临时标签后，严格大于它的节点不可能改进 d(g)，
		// 立即停止；等于的节点仍需确定（可能是目标的紧边前驱），
		// 故弹出集合恰是 d(x)<=d(g) 的确定节点的子集。
		if x != g && dist[g] < inf && dist[x] > dist[g] {
			break
		}
		settled[x] = true
		poppedOrder = append(poppedOrder, x)
		if x == g {
			break
		}
		for _, ce := range adj[x] {
			if settled[ce.v] {
				continue
			}
			a, ok := ce.arrival(dist[x])
			if ok && a < dist[ce.v] {
				dist[ce.v] = a
				h.push(ce.v)
			}
		}
	}

	if !settled[g] {
		return nil, errorf(ErrUnreachable, "timetable: node %d unreachable from %d at t0=%d (ver=%d)", g, s, t0, v)
	}

	byID := make(map[int]*cedge, len(edges))
	incoming := make([][]*cedge, n)
	for _, ce := range edges {
		byID[ce.id] = ce
		if settled[ce.u] && settled[ce.v] {
			incoming[ce.v] = append(incoming[ce.v], ce)
		}
	}

	// 路径选择：先最少边数，再边编号序列字典序最小。
	// 阶段 A：只在紧边 DAG 上求最少边数（弹出顺序保证前驱先处理）。
	hops := make([]int, n)
	for i := range hops {
		hops[i] = -1
	}
	hops[s] = 0
	for i := 1; i < len(poppedOrder); i++ {
		x := poppedOrder[i]
		bestHops := 1 << 30
		for _, ce := range incoming[x] {
			a := ce.u
			if hops[a] < 0 {
				continue
			}
			_, arr, ok := ce.earliestDeparture(dist[a])
			if !ok || arr != dist[x] {
				continue // 非紧边
			}
			if hh := hops[a] + 1; hh < bestHops {
				bestHops = hh
			}
		}
		if bestHops == 1<<30 {
			continue
		}
		hops[x] = bestHops
	}

	// 阶段 B：按边数从小到大分层。在第 L 层，每个节点在最少边数的
	// 紧边入边中选 (前驱秩, 末边编号) 最小者作为其键；该层全部键
	// 排序去重后分配的秩即等长边编号序列的字典序。
	ranks := make([]int, n)
	predEdge := make([]int, n)
	predNode := make([]int, n)
	ranks[s] = 0
	layerNodes := make([][]int, 1)
	maxHop := 0
	for _, x := range poppedOrder[1:] {
		if hops[x] > 0 {
			for len(layerNodes) <= hops[x] {
				layerNodes = append(layerNodes, nil)
			}
			layerNodes[hops[x]] = append(layerNodes[hops[x]], x)
			if hops[x] > maxHop {
				maxHop = hops[x]
			}
		}
	}
	type nodeKey struct {
		node int
		key  pathKey
	}
	for L := 1; L <= maxHop; L++ {
		nks := make([]nodeKey, 0, len(layerNodes[L]))
		for _, x := range layerNodes[L] {
			var bestKey pathKey
			bestEdge, bestPred, have := 0, -1, false
			for _, ce := range incoming[x] {
				a := ce.u
				if hops[a] != L-1 {
					continue
				}
				_, arr, ok := ce.earliestDeparture(dist[a])
				if !ok || arr != dist[x] {
					continue
				}
				k := pathKey{rank: ranks[a], edge: ce.id}
				if !have || lessKey(k, bestKey) {
					bestKey, bestEdge, bestPred, have = k, ce.id, a, true
				}
			}
			if have {
				predEdge[x], predNode[x] = bestEdge, bestPred
				nks = append(nks, nodeKey{x, bestKey})
			}
		}
		sort.Slice(nks, func(i, j int) bool { return lessKey(nks[i].key, nks[j].key) })
		assignRank := -1
		var prev pathKey
		for i, nk := range nks {
			if i == 0 || lessKey(prev, nk.key) {
				assignRank++
				prev = nk.key
			}
			ranks[nk.node] = assignRank
		}
	}

	route := []Leg{}
	if g != s {
		if hops[g] < 0 {
			return nil, errorf(ErrUnreachable, "timetable: internal: no tight-edge path to settled target")
		}
		reversed := make([]Leg, 0, hops[g])
		for x := g; x != s; {
			ce := byID[predEdge[x]]
			dep, arr, ok := ce.earliestDeparture(dist[ce.u])
			if !ok || arr != dist[x] {
				return nil, errorf(ErrUnreachable, "timetable: internal: reconstructed leg is not tight")
			}
			reversed = append(reversed, Leg{Edge: ce.id, Dep: dep, Arr: arr})
			x = predNode[x]
		}
		route = make([]Leg, len(reversed))
		for i, lg := range reversed {
			route[len(reversed)-1-i] = lg
		}
	}

	return &Result{Arrival: dist[g], Route: route, Popped: len(poppedOrder)}, nil
}

// fullDistances 供测试校验：不做早停，求 s 出发的全部最早到达标签
// （不可达为 -1）。用于独立核对 popped 的上界语义。
func (nw *Network) fullDistances(s int, t0 int64, ver int) []int64 {
	nw.mu.RLock()
	edges := nw.snapshot(ver)
	n := nw.n
	nw.mu.RUnlock()
	adj := make([][]*cedge, n)
	for _, ce := range edges {
		adj[ce.u] = append(adj[ce.u], ce)
	}
	dist := make([]int64, n)
	settled := make([]bool, n)
	for i := range dist {
		dist[i] = inf
	}
	dist[s] = t0
	h := &heap{dist: dist, nodes: make([]int, 0, 64)}
	h.push(s)
	for len(h.nodes) > 0 {
		x := h.pop()
		if settled[x] {
			continue
		}
		settled[x] = true
		for _, ce := range adj[x] {
			if settled[ce.v] {
				continue
			}
			a, ok := ce.arrival(dist[x])
			if ok && a < dist[ce.v] {
				dist[ce.v] = a
				h.push(ce.v)
			}
		}
	}
	out := make([]int64, n)
	for i, d := range dist {
		if !settled[i] {
			out[i] = -1
		} else {
			out[i] = d
		}
	}
	return out
}
