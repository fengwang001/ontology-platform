// Package km 用 Kuhn–Munkres 算法求带权完全二分图的最大权完美匹配，多最优时取字典序最小。
package km

import (
	"errors"
	"ontology/bmg"
	"slices"
	"sync/atomic"
)

var ErrIncomplete = errors.New("km: weight matrix is incomplete")

type pq struct { // 索引最小堆：key[j]=slack+累计偏移，每列一条，取 delta 只弹 1 个右节点
	j, pos []int
	key    []int64
}

func newPQ(n int, key []int64) *pq {
	p := &pq{j: make([]int, n), pos: make([]int, n), key: key}
	for i := n - 1; i >= 0; i-- { // 降序：先放恒等键，内部节点顺带堆化
		p.j[i], p.pos[i] = i, i
		if i < n/2 {
			p.down(i, n)
		}
	}
	return p
}
func (p *pq) less(a, b int) bool { return p.key[p.j[a]] < p.key[p.j[b]] }
func (p *pq) sw(a, b int)        { p.j[a], p.j[b] = p.j[b], p.j[a]; p.pos[p.j[a]], p.pos[p.j[b]] = a, b }
func (p *pq) up(i int) {
	for i > 0 && p.less(i, (i-1)/2) {
		p.sw(i, (i-1)/2)
		i = (i - 1) / 2
	}
}
func (p *pq) down(i, n int) {
	for c := 2*i + 1; c < n; c = 2*i + 1 {
		if c+1 < n && p.less(c+1, c) {
			c++
		}
		if !p.less(c, i) {
			return
		}
		p.sw(i, c)
		i = c
	}
}
func (p *pq) pop() int {
	top, last := p.j[0], len(p.j)-1
	p.j[0] = p.j[last]
	p.pos[p.j[0]] = 0
	p.j = p.j[:last]
	p.down(0, last) // last=0 时无子节点，自然空转
	p.pos[top] = -1
	return top
}
func (p *pq) dec(j int) { p.up(p.pos[j]) }

type Solver struct{ lastDeltaChecks atomic.Int64 } // 非导出计数器：最近一次求 delta 弹出的右节点数，仅供包内白盒测试
func New() *Solver                                 { return &Solver{} }
func (s *Solver) checks() int64                    { return s.lastDeltaChecks.Load() }
func (s *Solver) Solve(g *bmg.Graph) (int64, []int, error) {
	if !g.AllSet() {
		return 0, nil, ErrIncomplete
	}
	n := g.N()
	w := func(i, j int) int64 { v, _ := g.Weight(i, j); return v }
	l := make([]int64, n) // 左标号=行最大；右标号 0；标号恒可行 l+r>=w
	for i := range l {
		l[i] = slices.Max(g.Row(i))
	}
	mL, mR, r := make([]int, n), make([]int, n), make([]int64, n)
	for root := 0; root < n; root++ {
		augment(s, w, n, root, l, r, mL, mR)
	}
	m := lexMin(w, n, l, r)
	var V int64
	for i := range m {
		V += w(i, m[i])
	}
	return V, m, nil
}

func augment(s *Solver, w func(int, int) int64, n, root int, l, r []int64, mL, mR []int) {
	slack, raw, way := make([]int64, n), make([]int64, n), make([]int, n)
	for j := range slack {
		v := l[root] + r[j] - w(root, j)
		slack[j], raw[j], way[j] = v, v, root // slack=min_{i∈S}(l+r-w)，raw=slack+off
	}
	p, inT, S, off := newPQ(n, raw), make([]bool, n), []int{root}, int64(0)
	for {
		s.lastDeltaChecks.Store(1)
		j0 := p.pop()
		d := raw[j0] - off // delta：树内左到树外右的最小 slack
		off += d
		for _, u := range S {
			l[u] -= d
		}
		for j := 0; j < n; j++ {
			if inT[j] {
				r[j] += d
			} else if j != j0 {
				slack[j] -= d // 树外右 slack -d（raw 不变）
			}
		}
		inT[j0] = true
		if mR[j0] == 0 { // 到达空闲右节点：沿 way 翻转增广路
			for col := j0; col >= 0; {
				row := way[col]
				mL[row], mR[col], col = col+1, row+1, mL[row]-1
			}
			return
		}
		u := mR[j0] - 1
		S = append(S, u) // 匹配边把新左节点带入增广树
		for j := 0; j < n; j++ {
			if v := l[u] + r[j] - w(u, j); !inT[j] && v < slack[j] {
				slack[j], raw[j], way[j] = v, v+off, u
				p.dec(j)
			}
		}
	}
}

// lexMin：行逆序 n-1..0 在相等子图上做 Kuhn 增广、邻居按列号升序，得字典序最小匹配。
func lexMin(w func(int, int) int64, n int, l, r []int64) []int {
	owner := make([]int, n) // 存“行号+1”，零值即该列未被占用
	var dfs func(u int, seen []bool) bool
	dfs = func(u int, seen []bool) bool {
		for j := 0; j < n; j++ {
			if l[u]+r[j] != w(u, j) || seen[j] {
				continue
			}
			seen[j] = true
			if v := owner[j]; v == 0 || dfs(v-1, seen) {
				owner[j] = u + 1
				return true
			}
		}
		return false
	}
	for i := n - 1; i >= 0; i-- {
		dfs(i, make([]bool, n))
	}
	m := make([]int, n)
	for j, v := range owner {
		m[v-1] = j
	}
	return m
}
