// Package mcs 用最大基数搜索（MCS）求完美消除序（PEO）并判定弦图。依赖 ug。
package mcs

import (
	"container/heap"

	"ontology/ug"
)

// entry 是堆中的一项候选；w 是入堆时的权重，过期项靠与当前权重比对剔除。
type entry struct{ v, w int }

// candHeap 按（权重降序、编号升序）取最大，即 MCS 的选取规则（并列取最小编号）。
type candHeap []entry

func (h candHeap) Len() int { return len(h) }
func (h candHeap) Less(i, j int) bool {
	if h[i].w != h[j].w {
		return h[i].w > h[j].w
	}
	return h[i].v < h[j].v
}
func (h candHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *candHeap) Push(x any)   { *h = append(*h, x.(entry)) }
func (h *candHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// Solver 对一张固定的图跑一次 MCS + 成团检查。
type Solver struct {
	g        *ug.Graph
	peo      []int
	chordal  bool
	violator int

	// 以下为一次 MCS 过程的工作状态；probes 记录最近一次选出节点时检查过的
	// 候选堆项个数（含过期项），非导出，仅供包内测试验证堆实现。
	weight   []int
	selected []bool
	h        candHeap
	probes   int
}

// New 绑定图 g；g 在 Compute 期间不得被修改。
func New(g *ug.Graph) *Solver { return &Solver{g: g, violator: -1} }

// start 初始化权重与候选堆，所有节点权重为 0。
func (s *Solver) start() {
	n := s.g.N()
	s.weight = make([]int, n)
	s.selected = make([]bool, n)
	s.h = make(candHeap, 0, n)
	for v := 0; v < n; v++ {
		s.h = append(s.h, entry{v, 0})
	}
	heap.Init(&s.h)
}

// step 选出下一个节点并更新其未选邻居的权重，返回选中节点。
// 选点过程只检查堆顶附近的候选（过期项），不扫描全表。
func (s *Solver) step() int {
	s.probes = 0
	for {
		s.probes++
		e := heap.Pop(&s.h).(entry)
		if s.selected[e.v] || e.w != s.weight[e.v] {
			continue // 过期堆项
		}
		s.selected[e.v] = true
		for _, w := range s.g.Neighbors(e.v) {
			if !s.selected[w] {
				s.weight[w]++
				heap.Push(&s.h, entry{w, s.weight[w]})
			}
		}
		return e.v
	}
}

// Compute 跑完整 MCS，把选中顺序取逆得到 PEO，再做逐节点成团检查。
func (s *Solver) Compute() {
	s.start()
	n := s.g.N()
	order := make([]int, 0, n)
	for len(order) < n {
		order = append(order, s.step())
	}
	s.peo = make([]int, n)
	for i, v := range order {
		s.peo[n-1-i] = v
	}
	s.check()
}

// check 对 PEO 中每个节点 v 检查 N+(v)（PEO 里排在 v 之后的邻居）是否两两相邻，
// 记录 PEO 中最靠前的不成团节点。
func (s *Solver) check() {
	n := s.g.N()
	pos := make([]int, n)
	for i, v := range s.peo {
		pos[v] = i
	}
	s.chordal = true
	s.violator = -1
	for _, v := range s.peo {
		var later []int
		for _, w := range s.g.Neighbors(v) {
			if pos[w] > pos[v] {
				later = append(later, w)
			}
		}
		ok := true
		for i := 0; i < len(later) && ok; i++ {
			for j := i + 1; j < len(later); j++ {
				if !s.g.HasEdge(later[i], later[j]) {
					ok = false
					break
				}
			}
		}
		if !ok {
			s.chordal = false
			s.violator = v
			return
		}
	}
}

// PEO 返回完美消除序（MCS 选中序的逆序）的副本。
func (s *Solver) PEO() []int { return append([]int(nil), s.peo...) }

// IsChordal 报告图是否弦图。
func (s *Solver) IsChordal() bool { return s.chordal }

// FirstViolator 返回 PEO 中最靠前的不成团节点；弦图返回 -1。
func (s *Solver) FirstViolator() int { return s.violator }
