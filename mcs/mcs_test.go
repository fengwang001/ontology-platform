package mcs

import (
	"math/rand/v2"
	"ontology/ug"
	"slices"
	"testing"
)

func naivePEO(g *ug.Graph) []int {
	n := g.N()
	w, sel, peo := make([]int, n), make([]bool, n), make([]int, n)
	for i := 0; i < n; i++ {
		best := -1
		for v := 0; v < n; v++ {
			if !sel[v] && (best < 0 || w[v] > w[best]) {
				best = v
			}
		}
		sel[best] = true
		peo[n-1-i] = best
		for _, u := range g.Neighbors(best) {
			w[u]++ // 已选节点的权重不再被读取
		}
	}
	return peo
}
func naiveLaterClique(g *ug.Graph, peo []int, v int) bool {
	pos := make([]int, g.N())
	for i, x := range peo {
		pos[x] = i
	}
	nb := g.Neighbors(v)
	for i, a := range nb {
		for _, b := range nb[i+1:] {
			if pos[a] > pos[v] && pos[b] > pos[v] && !g.HasEdge(a, b) {
				return false
			}
		}
	}
	return true
}
func naiveCheck(g *ug.Graph, peo []int) (bool, int) {
	for _, v := range peo {
		if !naiveLaterClique(g, peo, v) {
			return false, v
		}
	}
	return true, -1
}
func randGraph(n, m int, seed uint64) *ug.Graph {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
	var all [][2]int
	for u := 0; u < n; u++ {
		for v := u + 1; v < n; v++ {
			all = append(all, [2]int{u, v})
		}
	}
	r.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	g := ug.New(n)
	for _, e := range all[:min(m, len(all))] {
		_ = g.AddEdge(e[0], e[1])
	}
	return g
}
func eachGraph(f func(*ug.Graph, *Solver)) {
	for _, c := range []struct{ n, m int }{{4, 4}, {6, 6}, {10, 20}, {25, 50}, {50, 120}} {
		for seed := uint64(0); seed < 6; seed++ {
			g := randGraph(c.n, c.m, seed*7+uint64(c.n+c.m))
			s := New(g)
			s.Compute()
			f(g, s)
		}
	}
}
func TestKnownGraphs(t *testing.T) {
	cases := []struct {
		n     int
		edges [][2]int
		ok    bool
		vio   int
	}{
		{6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {3, 4}, {2, 5}}, true, -1},
		{4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 0}}, false, 3},
		{5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 0}}, false, 4},
		{4, [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}, true, -1},
		{0, nil, true, -1},
		{1, nil, true, -1},
	}
	for i, c := range cases {
		g := ug.New(c.n)
		for _, e := range c.edges {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		s := New(g)
		s.Compute()
		if s.IsChordal() != c.ok || s.FirstViolator() != c.vio {
			t.Errorf("case %d: got chordal=%v violator=%d", i, s.IsChordal(), s.FirstViolator())
		}
	}
}
func TestPEOMatchesNaiveReference(t *testing.T) {
	eachGraph(func(g *ug.Graph, s *Solver) {
		if want := naivePEO(g); !slices.Equal(s.PEO(), want) {
			t.Fatalf("PEO %v != 朴素参照 %v", s.PEO(), want)
		}
	})
}
func TestChordalVersusNaive(t *testing.T) {
	eachGraph(func(g *ug.Graph, s *Solver) {
		ok, vio := naiveCheck(g, s.PEO())
		if s.IsChordal() != ok || s.FirstViolator() != vio {
			t.Fatalf("got (%v,%d), 朴素参照 (%v,%d)", s.IsChordal(), s.FirstViolator(), ok, vio)
		}
	})
}

// TestCliqueCheckEqualsNaive 逐节点核对：同一 PEO 下违规者之前全部成团、违规者不成团。
func TestCliqueCheckEqualsNaive(t *testing.T) {
	eachGraph(func(g *ug.Graph, s *Solver) {
		peo := s.PEO()
		vioIdx := slices.Index(peo, s.FirstViolator())
		for i, v := range peo {
			ok := naiveLaterClique(g, peo, v)
			if (s.IsChordal() || i < vioIdx) && !ok {
				t.Fatalf("节点 %d 应成团而朴素检查不成团", v)
			}
			if i == vioIdx && ok {
				t.Fatalf("违规节点 %d 朴素检查却成团", v)
			}
		}
	})
}

// TestProbeBoundIsolatedNodes 证明选点用堆而非全表扫描：每步检查候选数 ≤2，与 m 无关。
func TestProbeBoundIsolatedNodes(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New(ug.New(m))
		s.start()
		for i := 0; i < m; i++ {
			if s.step(); s.probes > 2 {
				t.Fatalf("m=%d 第 %d 步检查 %d 个候选，疑似全表扫描", m, i, s.probes)
			}
		}
	}
}
