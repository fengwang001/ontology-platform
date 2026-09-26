package mcs

import (
	"math/rand"
	"slices"
	"testing"

	"ontology/ug"
)

// naiveMCS 朴素参照：每步全表扫描所有未选节点取 (权重最大, 编号最小)。
func naiveMCS(g *ug.Graph) []int {
	n := g.N()
	w := make([]int, n)
	sel := make([]bool, n)
	order := make([]int, 0, n)
	for len(order) < n {
		best := -1
		for v := 0; v < n; v++ {
			if !sel[v] && (best < 0 || w[v] > w[best]) {
				best = v
			}
		}
		sel[best] = true
		order = append(order, best)
		for _, u := range g.Neighbors(best) {
			if !sel[u] {
				w[u]++
			}
		}
	}
	return order
}

// naiveChordal 朴素参照：同一 PEO 下逐节点对 N+(v) 两两查边，返回首个违规节点。
func naiveChordal(g *ug.Graph, peo []int) int {
	rank := make([]int, len(peo))
	for i, v := range peo {
		rank[v] = i
	}
	for _, v := range peo {
		var later []int
		for _, u := range g.Neighbors(v) {
			if rank[u] > rank[v] {
				later = append(later, u)
			}
		}
		for i := 0; i < len(later); i++ {
			for j := i + 1; j < len(later); j++ {
				if !g.HasEdge(later[i], later[j]) {
					return v
				}
			}
		}
	}
	return -1
}

// randGraph 以 seed 生成 n 节点、约 m 条边的随机图（加边顺序也随机）。
func randGraph(t *testing.T, n, m int, seed int64) *ug.Graph {
	t.Helper()
	g, err := ug.New(n)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(seed))
	for added := 0; added < m; {
		u, v := r.Intn(n), r.Intn(n)
		if u != v && g.AddEdge(u, v) == nil {
			added++
		}
	}
	return g
}

// TestPEOMatchesNaiveMCS 不变量 1：PEO 必须等于朴素 MCS 选中序的逆序。
func TestPEOMatchesNaiveMCS(t *testing.T) {
	cases := []struct{ n, m int }{{1, 0}, {2, 1}, {6, 6}, {10, 12}, {30, 60}, {80, 200}}
	for _, c := range cases {
		for seed := int64(0); seed < 5; seed++ {
			g := randGraph(t, c.n, c.m, seed*1000+int64(c.n))
			var s Solver
			got := s.Compute(g).PEO
			order := naiveMCS(g)
			slices.Reverse(order)
			if !slices.Equal(got, order) {
				t.Fatalf("n=%d m=%d seed=%d: PEO %v != naive reverse %v", c.n, c.m, seed, got, order)
			}
		}
	}
}

// TestChordalMatchesNaive 不变量 2+3：判定与首违规节点同朴素参照逐图一致。
func TestChordalMatchesNaive(t *testing.T) {
	cases := []struct{ n, m int }{{1, 0}, {4, 4}, {6, 6}, {10, 15}, {30, 70}, {60, 150}}
	for _, c := range cases {
		for seed := int64(0); seed < 8; seed++ {
			g := randGraph(t, c.n, c.m, seed*77+int64(c.m))
			var s Solver
			r := s.Compute(g)
			wantV := naiveChordal(g, r.PEO)
			if r.Violator != wantV || r.Chordal != (wantV == -1) {
				t.Fatalf("n=%d m=%d seed=%d: got (%v,%d) want violator %d",
					c.n, c.m, seed, r.Chordal, r.Violator, wantV)
			}
		}
	}
}

// TestCandidateChecksBounded 复杂度：m 个孤立节点，每步选节点检查的候选数 ≤2，
// 不随 m 线性增长（同包白盒读非导出计数器 checked）。
func TestCandidateChecksBounded(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		g, err := ug.New(m)
		if err != nil {
			t.Fatal(err)
		}
		var s Solver
		r := s.Compute(g)
		if !r.Chordal || len(r.PEO) != m {
			t.Fatalf("m=%d: bad result %+v", m, r)
		}
		if s.checked > 2 {
			t.Fatalf("m=%d: checked %d candidates in one selection, want <=2", m, s.checked)
		}
	}
}
