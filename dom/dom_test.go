package dom

import (
	"math/rand/v2"
	"testing"

	"ontology/dgraph"
)

func randGraph(seed int64) *dgraph.Graph {
	r := rand.New(rand.NewPCG(uint64(seed), 0))
	n := 4 + r.IntN(16)
	g, _ := dgraph.New(n)
	for range n * n / 2 {
		g.AddEdge(r.IntN(n), r.IntN(n)) // 自环/重复边被拒属正常
	}
	return g
}

// naiveIDoms 朴素定点迭代参照：doms[v]={v}∪∩doms[p]，再取最近支配者。
func naiveIDoms(g *dgraph.Graph) []int {
	n := g.N()
	reach := g.Reachable()
	preds := g.Preds()
	var full uint64
	doms := make([]uint64, n)
	for v := range n {
		if reach[v] {
			full, doms[v] = full|1<<v, ^uint64(0)
		}
	}
	doms[0] = 1
	for changed := true; changed; {
		changed = false
		for v := 1; v < n; v++ {
			if !reach[v] {
				continue
			}
			s := full | 1<<v
			for _, p := range preds[v] {
				if reach[p] { // 不可达前驱不产生从 0 出发的路径，跳过
					s &= doms[p] | 1<<v
				}
			}
			if s != doms[v] {
				doms[v] = s
				changed = true
			}
		}
	}
	out := make([]int, n)
	for v := range out {
		rest := doms[v] &^ (1 << v)
		out[v] = int(doms[v]) - 1 // 不可达 -1；入口 0
		for c := 0; c < n && rest != 0; c++ {
			if rest&(1<<c) != 0 && rest&^(1<<c)&^doms[c] == 0 {
				out[v] = c
			}
		}
	}
	return out
}

func reaches(g *dgraph.Graph, src, dst, skip int) bool {
	vis := map[int]bool{src: true}
	for st := []int{src}; len(st) > 0 && src != skip; {
		v := st[len(st)-1]
		st = st[:len(st)-1]
		if v == dst {
			return true
		}
		for _, w := range g.Succ(v) {
			if w != skip && !vis[w] {
				vis[w] = true
				st = append(st, w)
			}
		}
	}
	return false
}

// bruteDominates 支配的参照定义：b 可达，且删掉 a 后 0 到 b 不再可达。
func bruteDominates(g *dgraph.Graph, a, b int) bool {
	return reaches(g, 0, b, -1) && (a == b || !reaches(g, 0, b, a))
}

func TestIDomMatchesNaive(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		g := randGraph(seed)
		d := Compute(g)
		want := naiveIDoms(g)
		for v := 0; v < g.N(); v++ {
			if got := d.IDom(v); got != want[v] {
				t.Fatalf("seed %d: IDom(%d)=%d, naive=%d", seed, v, got, want[v])
			}
		}
	}
}

func TestDominatesBruteForce(t *testing.T) {
	for seed := int64(100); seed < 120; seed++ {
		g := randGraph(seed)
		d := Compute(g)
		for a := 0; a < g.N(); a++ {
			for b := 0; b < g.N(); b++ {
				if got, want := d.Dominates(a, b), bruteDominates(g, a, b); got != want {
					t.Fatalf("seed %d: Dominates(%d,%d)=%v, def=%v", seed, a, b, got, want)
				}
			}
		}
	}
}

func TestIDomIsImmediate(t *testing.T) {
	for seed := int64(200); seed < 220; seed++ {
		g := randGraph(seed)
		d := Compute(g)
		for v := 0; v < g.N(); v++ {
			id := d.IDom(v)
			if id < 0 || v == 0 {
				continue
			}
			if !bruteDominates(g, id, v) {
				t.Fatalf("seed %d: idom(%d)=%d does not dominate v", seed, v, id)
			}
			for u := 0; u < g.N(); u++ {
				if u != v && u != id && bruteDominates(g, u, v) && !bruteDominates(g, u, id) {
					t.Fatalf("seed %d: dominator %d of %d does not dominate idom %d", seed, u, v, id)
				}
			}
		}
	}
}

func TestDominatesCheckedBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g, _ := dgraph.New(m)
		for i := 0; i+1 < m; i++ {
			if err := g.AddEdge(i, i+1); err != nil {
				t.Fatal(err)
			}
		}
		d := Compute(g)
		for q := 0; q < 5; q++ {
			if !d.Dominates(0, m-1) || d.checked.Load() > 2 {
				t.Fatalf("m=%d: dominates=%v checked=%d, want true & <=2", m, d.Dominates(0, m-1), d.checked.Load())
			}
		}
	}
}
