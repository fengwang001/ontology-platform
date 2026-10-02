package sps

import "sort"

// naiveEdge 是朴素参考模型中的边。
type naiveEdge struct {
	id   int
	u, v int
	w    int64
}

// naiveModel 每次更新后整图重算距离与父边，作为正确性基准。
type naiveModel struct {
	n, s    int
	edges   map[int]naiveEdge
	liveIDs []int
	dist    []int64
	par     []int
	version int
	hist    map[int][]int64 // 版本 -> 距离快照（朴素模型全量保留）
}

func newNaive(n, s int) *naiveModel {
	m := &naiveModel{n: n, s: s, edges: map[int]naiveEdge{}, dist: make([]int64, n), par: make([]int, n), hist: map[int][]int64{}}
	for i := range m.dist {
		m.dist[i] = inf
	}
	m.dist[s] = 0
	m.hist[0] = m.snapshotDist()
	return m
}

func (m *naiveModel) add(id, u, v int, w int64) {
	m.edges[id] = naiveEdge{id, u, v, w}
	m.liveIDs = append(m.liveIDs, id)
	m.recompute()
	m.version++
	m.hist[m.version] = m.snapshotDist()
}

func (m *naiveModel) setWeight(id int, w int64) bool {
	e, ok := m.edges[id]
	if !ok {
		return false
	}
	e.w = w
	m.edges[id] = e
	m.recompute()
	m.version++
	m.hist[m.version] = m.snapshotDist()
	return true
}

func (m *naiveModel) remove(id int) bool {
	if _, ok := m.edges[id]; !ok {
		return false
	}
	delete(m.edges, id)
	for i, x := range m.liveIDs {
		if x == id {
			m.liveIDs = append(m.liveIDs[:i], m.liveIDs[i+1:]...)
			break
		}
	}
	m.recompute()
	m.version++
	m.hist[m.version] = m.snapshotDist()
	return true
}

func (m *naiveModel) recompute() {
	const pinf = int64(1<<62 - 1)
	d := make([]int64, m.n)
	for i := range d {
		d[i] = pinf
	}
	d[m.s] = 0
	// Dijkstra
	type qi struct {
		x  int
		dd int64
	}
	heap := []qi{{m.s, 0}}
	for len(heap) > 0 {
		best := 0
		for i := 1; i < len(heap); i++ {
			if heap[i].dd < heap[best].dd {
				best = i
			}
		}
		cur := heap[best]
		heap = append(heap[:best], heap[best+1:]...)
		if cur.dd != d[cur.x] {
			continue
		}
		for _, e := range m.edges {
			if e.u == cur.x && cur.dd+e.w < d[e.v] {
				d[e.v] = cur.dd + e.w
				heap = append(heap, qi{e.v, d[e.v]})
			}
		}
	}
	m.dist = d
	// 父边：编号最小的紧边
	ids := make([]int, 0, len(m.edges))
	for id := range m.edges {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	m.par = make([]int, m.n)
	for _, id := range ids {
		e := m.edges[id]
		if e.u != e.v && e.v != m.s && d[e.u] != pinf && d[e.v] != pinf && d[e.u]+e.w == d[e.v] && m.par[e.v] == 0 {
			m.par[e.v] = id
		}
	}
	if m.par[m.s] != 0 {
		panic("naive assigned a parent edge to the source")
	}
}

func (m *naiveModel) snapshotDist() []int64 {
	out := make([]int64, m.n)
	copy(out, m.dist)
	return out
}

func (m *naiveModel) snapshotPar() []int {
	out := make([]int, m.n)
	copy(out, m.par)
	return out
}
