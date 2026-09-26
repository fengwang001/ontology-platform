package km

import (
	"container/heap"
	"slices"

	"ontology/bmg"
)

type slHeap struct { // 按 slack 取值的最小堆；off 惰性吸收每次树外 slack 统一减 d
	j   []int
	st  []int64 // 存储值；实际 slack = st - off
	pos []int   // pos[j] 在堆中下标，-1 不在堆
	off int64
}

func (h *slHeap) Len() int { return len(h.j) }
func (h *slHeap) Less(a, b int) bool {
	return h.st[a] < h.st[b] || (h.st[a] == h.st[b] && h.j[a] < h.j[b])
}
func (h *slHeap) Swap(a, b int) {
	h.j[a], h.j[b] = h.j[b], h.j[a]
	h.st[a], h.st[b] = h.st[b], h.st[a]
	h.pos[h.j[a]], h.pos[h.j[b]] = a, b
}
func (h *slHeap) Push(x any) {
	j := x.(int)
	h.pos[j], h.j, h.st = len(h.j), append(h.j, j), append(h.st, 0)
}
func (h *slHeap) Pop() any {
	k := len(h.j) - 1
	j := h.j[k]
	h.j, h.st, h.pos[j] = h.j[:k], h.st[:k], -1
	return j
}
func newSlHeap(n int) *slHeap {
	return &slHeap{pos: slices.Repeat([]int{-1}, n)}
}
func (h *slHeap) fill(n int, inT []bool, slack []int64) { // 首次求 delta 时装入并 O(n) 堆化
	for j := 0; j < n; j++ {
		if !inT[j] {
			h.pos[j] = len(h.j)
			h.j, h.st = append(h.j, j), append(h.st, slack[j])
		}
	}
	heap.Init(h)
}
func (h *slHeap) slackTop() int64 { return h.st[0] - h.off } // 求 delta 只看堆顶 1 个
func (h *slHeap) has(j int) bool  { return h.pos[j] >= 0 }
func (h *slHeap) bump(d int64)    { h.off += d }
func (h *slHeap) eff(j int) int64 { return h.st[h.pos[j]] - h.off }
func (h *slHeap) setEff(j int, v int64) {
	h.st[h.pos[j]] = v + h.off
	heap.Fix(h, h.pos[j])
}
func (h *slHeap) remove(j int) { heap.Remove(h, h.pos[j]) }
func (h *slHeap) dropZeros(cb func(int)) { // 弹出调标号后归零者，逐个纳入增广树
	for h.Len() > 0 && h.st[0]-h.off == 0 {
		cb(heap.Pop(h).(int))
	}
}

func (s *Solver) lexMin(m []int) { // 沿紧边交错圈逐位旋转取字典序最小；圈节点均 >start
	n := len(m)
	tightR := make([][]int, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if w, _ := s.g.Weight(i, j); s.labelL[i]+s.labelR[j] == w {
				tightR[i] = append(tightR[i], j)
			}
		}
	}
	inv := make([]int, n) // 右→左
	for i, j := range m {
		inv[j] = i
	}
	rotate := func(start int) bool {
		best := m[start]
		var bestPath []int
		for _, j0 := range tightR[start] {
			v := inv[j0]
			if v <= start || m[v] >= best {
				continue
			}
			seen := map[int]bool{start: true}
			path := []int{v}
			var dfs func(int) bool
			dfs = func(u int) bool {
				seen[u] = true
				for _, j := range tightR[u] {
					w := inv[j]
					if w == start { // 经由 m[start] 闭合
						return true
					}
					if w > start && !seen[w] {
						path = append(path, w)
						if dfs(w) {
							return true
						}
						path = path[:len(path)-1]
					}
				}
				return false
			}
			if dfs(v) && m[v] < best {
				best, bestPath = m[v], append([]int(nil), path...)
			}
		}
		if bestPath == nil {
			return false
		}
		t, prev := m[start], start // start←m[path0]←…←m[start]
		for _, u := range bestPath {
			m[prev] = m[u]
			inv[m[prev]], prev = prev, u
		}
		m[prev], inv[t] = t, prev
		return true
	}
	for changed := true; changed; {
		changed = false
		for i := 0; i < n; i++ {
			if rotate(i) {
				changed = true
				break
			}
		}
	}
}
func VerifyDeltaBound(sizes []int, bound int) bool { // 只回 bool，不泄露计数
	for _, n := range sizes {
		g := bmg.New(n)
		for l := 0; l < n; l++ {
			for r := 0; r < n; r++ {
				w := int64(-1)
				if r == 0 && l < 2 { // 仅 (0,0)、(1,0) 为 0
					w = 0
				}
				_ = g.SetWeight(l, r, w)
			}
		}
		s := NewSolver(g)
		s.augment(0) // 左0 直接匹配右0，无需 delta
		s.augment(1) // 右0 被占，须求一次 delta=1 腾出空闲右节点
		if s.deltaRMax == 0 || s.deltaRMax > bound {
			return false
		}
	}
	return true
}
