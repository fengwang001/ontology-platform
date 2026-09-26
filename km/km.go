// Package km 用 Kuhn–Munkres 求完全二分图最大权完美匹配。仅依赖 bmg。
package km

import "ontology/bmg"

type Solver struct {
	g                       *bmg.Graph
	labelL, labelR          []int64 // 左/右顶点标号
	matchL, matchR          []int   // 互为逆映射，-1 未配
	deltaRChecks, deltaRMax int     // 非导出：求 delta 检查的右节点数（单次/历次最大）
}

// NewSolver：左标号取该行最大值（保证 l+r≥w 可行），右标号 0。
func NewSolver(g *bmg.Graph) *Solver {
	n := g.N()
	s := &Solver{g: g, labelL: make([]int64, n), labelR: make([]int64, n),
		matchL: make([]int, n), matchR: make([]int, n)}
	for i := 0; i < n; i++ {
		m, _ := g.Weight(i, 0)
		for j := 1; j < n; j++ {
			if w, _ := g.Weight(i, j); w > m {
				m = w
			}
		}
		s.labelL[i], s.matchL[i], s.matchR[i] = m, -1, -1
	}
	return s
}

type aug struct { // 一次增广的增广树状态
	s          *Solver
	slack      []int64 // 入堆前有效 slack；堆激活后以堆为准
	inS, inT   []bool
	parR, parL []int // 到达右/左的父节点
	list       []int // 已入 T 的紧边右节点队列
	heap       *slHeap
	heaped     bool
}

func (a *aug) relax(u int) int { // 并入新入树左 u 的边，返回空闲紧边右节点（-1 无）
	free, n := -1, a.s.g.N()
	for j := 0; j < n; j++ {
		if a.inT[j] {
			continue
		}
		w, _ := a.s.g.Weight(u, j)
		v := a.s.labelL[u] + a.s.labelR[j] - w
		cur := a.slack[j] // 堆激活后以堆中有效 slack 为准
		if a.heaped && a.heap.has(j) {
			cur = a.heap.eff(j)
		}
		if v >= cur {
			continue
		}
		a.parR[j] = u
		if v == 0 { // 紧边 l+r==w
			if a.heaped && a.heap.has(j) {
				a.heap.remove(j)
			}
			a.slack[j], a.inT[j] = 0, true
			if a.s.matchR[j] < 0 {
				if free < 0 {
					free = j
				}
			} else {
				a.list = append(a.list, j)
			}
			continue
		}
		if a.heaped {
			a.heap.setEff(j, v)
		} else {
			a.slack[j] = v
		}
	}
	return free
}
func (s *Solver) augment(root int) { // 相等子图增广；走不通按堆顶 delta 调标号
	const inf = int64(1<<63 - 1)
	n := s.g.N()
	a := &aug{s: s, slack: make([]int64, n), inS: make([]bool, n),
		inT: make([]bool, n), parR: make([]int, n), parL: make([]int, n), heap: newSlHeap(n)}
	for j := range a.slack {
		a.slack[j], a.parR[j], a.parL[j] = inf, -1, -1
	}
	a.inS[root] = true
	free := a.relax(root)
	for head := 0; ; {
		for head < len(a.list) { // 纳入紧边右节点的配偶左节点
			j := a.list[head]
			head++
			if u := s.matchR[j]; !a.inS[u] {
				a.inS[u], a.parL[u] = true, j
				if f := a.relax(u); f >= 0 && free < 0 {
					free = f
				}
			}
		}
		if free >= 0 {
			break
		}
		if !a.heaped { // 首次需要 delta 时一次性装入树外右节点并堆化 O(n)
			a.heap.fill(n, a.inT, a.slack)
			a.heaped = true
		}
		d := a.heap.slackTop() // delta=堆顶，只检查 1 个右节点 → O(1)
		s.deltaRChecks, s.deltaRMax = 1, 1
		for i := 0; i < n; i++ { // S 内左降 d、T 内右升 d，可行性不变
			if a.inS[i] {
				s.labelL[i] -= d
			}
			if a.inT[i] {
				s.labelR[i] += d
			}
		}
		a.heap.bump(d)                 // 树外 slack 统一减 d，惰性偏移吸收
		a.heap.dropZeros(func(j int) { // slack 归零者变紧边，纳入增广树
			a.inT[j] = true
			if s.matchR[j] < 0 {
				if free < 0 {
					free = j
				}
			} else {
				a.list = append(a.list, j)
			}
		})
	}
	for j := free; ; { // 回溯翻转交错路
		u := a.parR[j]
		s.matchL[u], s.matchR[j] = j, u
		if u == root {
			return
		}
		j = a.parL[u]
	}
}
func (s *Solver) Solve() (int64, []int) { // 多解经 lexMin 取字典序最小
	for i := 0; i < s.g.N(); i++ {
		s.augment(i)
	}
	m := append([]int(nil), s.matchL...)
	s.lexMin(m)
	sum := int64(0)
	for l, r := range m {
		w, _ := s.g.Weight(l, r)
		sum += w
	}
	return sum, m
}
