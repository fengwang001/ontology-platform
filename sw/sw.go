// Package sw 实现 Stoer-Wagner 全局最小割核心：最大邻接搜索（MAS）、
// 阶段割值计算、节点合并、对全部阶段取最小。依赖 wg。
package sw

import (
	"container/heap"

	"ontology/wg"
)

// entry 是惰性最大堆元素：node 当前到集合 A 的边权和为 w。
type entry struct {
	node int
	w    int64
}

// maxHeap 按 w 降序、并列按编号升序，保证「并列取编号最小」。
type maxHeap []entry

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].w != h[j].w {
		return h[i].w > h[j].w
	}
	return h[i].node < h[j].node
}
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)   { *h = append(*h, x.(entry)) }
func (h *maxHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// mas 是一次最大邻接搜索的状态。checked 记录最近一次 next 选出节点时
// 检查过的候选个数（含过期堆项），仅供包内测试核验堆的使用。
type mas struct {
	w       []map[int]int64
	inA     []bool
	cur     []int64
	h       maxHeap
	remain  int
	prev    int
	last    int
	checked int
}

// newMAS 以 start 为种子初始化一次 MAS；所有存活节点以权 0 入堆。
func newMAS(w []map[int]int64, alive []bool, start int) *mas {
	m := &mas{w: w, inA: make([]bool, len(w)), cur: make([]int64, len(w)),
		last: start, prev: -1}
	m.inA[start] = true
	for v := range w {
		if alive[v] && v != start {
			heap.Push(&m.h, entry{v, 0})
			m.remain++
		}
	}
	for v, wt := range w[start] {
		if alive[v] {
			m.cur[v] += wt
			heap.Push(&m.h, entry{v, m.cur[v]})
		}
	}
	return m
}

// next 选出并加入下一个节点：弹出堆顶直到命中有效项（未入 A 且权值最新）。
func (m *mas) next() (int, bool) {
	if m.remain == 0 {
		return -1, false
	}
	m.checked = 0
	for {
		e := heap.Pop(&m.h).(entry)
		m.checked++
		if m.inA[e.node] || e.w != m.cur[e.node] {
			continue // 过期堆项
		}
		m.inA[e.node] = true
		m.remain--
		m.prev, m.last = m.last, e.node
		for v, wt := range m.w[e.node] {
			if !m.inA[v] {
				m.cur[v] += wt
				heap.Push(&m.h, entry{v, m.cur[v]})
			}
		}
		return e.node, true
	}
}

// merge 把 t 并入 s：共同邻居的边权相加，删除 s-t 边与 t 的行。
func merge(w []map[int]int64, s, t int) {
	delete(w[s], t)
	for v, wt := range w[t] {
		if v == s {
			continue
		}
		w[s][v] += wt
		w[v][s] = w[s][v]
		delete(w[v], t)
	}
	w[t] = nil
}

// MinCut 返回 g 的全局最小割权值；孤立节点使割值为 0。
func MinCut(g *wg.Graph) int64 {
	n := g.N()
	w := make([]map[int]int64, n)
	alive := make([]bool, n)
	for i := range w {
		w[i] = map[int]int64{}
		alive[i] = true
	}
	g.ForEachEdge(func(u, v int, wt int64) {
		w[u][v], w[v][u] = wt, wt
	})
	best := int64(-1)
	for remain := n; remain > 1; remain-- {
		m := newMAS(w, alive, 0)
		for {
			if _, ok := m.next(); !ok {
				break
			}
		}
		s, t, cut := m.prev, m.last, m.cur[m.last]
		if best < 0 || cut < best {
			best = cut
		}
		merge(w, s, t)
		alive[t] = false
	}
	return best
}
