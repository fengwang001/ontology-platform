package closure

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"ontology/graph"
)

// pairs2 把 []graph.Edge 转成 [][2]string 以便与 graph.NaivePairs 比较。
func pairs2(ps []graph.Edge) [][2]string {
	out := make([][2]string, len(ps))
	for i, p := range ps {
		out[i] = [2]string{p.U, p.V}
	}
	return out
}

// TestTenSteps 钉住第三节十步推导：每步 |R|，删边步的 OD/RD，及关键步的精确 R。
func TestTenSteps(t *testing.T) {
	sizes := []int{1, 3, 3, 9, 9, 9, 6, 9, 5, 3}
	type dr struct{ od, rd int }
	wantDR := map[int]dr{6: {0, 0}, 7: {9, 6}, 9: {9, 5}, 10: {2, 0}}
	wantR := map[int][][2]string{
		7:  {{"A", "A"}, {"A", "C"}, {"B", "A"}, {"B", "C"}, {"C", "A"}, {"C", "C"}},
		9:  {{"A", "C"}, {"A", "D"}, {"B", "C"}, {"B", "D"}, {"C", "D"}},
		10: {{"A", "C"}, {"A", "D"}, {"C", "D"}},
	}
	ops := []struct {
		op   byte
		u, v string
	}{
		{'a', "A", "B"}, {'a', "B", "C"}, {'a', "A", "C"}, {'a', "C", "A"},
		{'a', "B", "C"}, {'r', "B", "C"}, {'r', "A", "B"}, {'a', "C", "D"},
		{'r', "C", "A"}, {'r', "B", "C"},
	}
	c := New()
	for i, o := range ops {
		if o.op == 'a' {
			c.AddEdge(o.u, o.v)
		} else {
			od, rd, _ := c.RemoveEdge(o.u, o.v)
			if w := wantDR[i+1]; od != w.od || rd != w.rd {
				t.Errorf("step %d: OD/RD = %d/%d, want %d/%d", i+1, od, rd, w.od, w.rd)
			}
		}
		if n := len(c.Pairs()); n != sizes[i] {
			t.Errorf("step %d: |R| = %d, want %d", i+1, n, sizes[i])
		}
		if w, ok := wantR[i+1]; ok && !slices.Equal(pairs2(c.Pairs()), w) {
			t.Errorf("step %d: R = %v, want %v", i+1, pairs2(c.Pairs()), w)
		}
	}
}

// TestRandomAgainstBFS 不变量1：多档规模随机增删，每步 R 与朴素 BFS 逐对一致。
func TestRandomAgainstBFS(t *testing.T) {
	for _, tc := range []struct{ seed, nodes, ops int }{{1, 4, 300}, {2, 6, 500}, {3, 8, 800}} {
		c := New()
		rng := rand.New(rand.NewPCG(uint64(tc.seed), 7))
		for i := 0; i < tc.ops; i++ {
			u := fmt.Sprintf("n%d", rng.IntN(tc.nodes))
			v := fmt.Sprintf("n%d", rng.IntN(tc.nodes))
			if c.HasEdge(u, v) && rng.IntN(2) == 0 {
				c.RemoveEdge(u, v)
			} else {
				c.AddEdge(u, v)
			}
			if got, want := pairs2(c.Pairs()), c.g.NaivePairs(); !slices.Equal(got, want) {
				t.Fatalf("seed %d op %d: R %v != naive %v", tc.seed, i, got, want)
			}
		}
	}
}

// TestMultiplicity 不变量2：重数 2→1 时边仍在、R 不变、不做过删；再删才 DRed。
func TestMultiplicity(t *testing.T) {
	c := New()
	c.AddEdge("a", "b")
	c.AddEdge("a", "b")
	if c.checked != 0 {
		t.Errorf("multiplicity increment checked %d pairs, want 0", c.checked)
	}
	before := c.Pairs()
	if od, rd, dropped := c.RemoveEdge("a", "b"); dropped || od != 0 || rd != 0 {
		t.Errorf("2->1: od/rd/dropped = %d/%d/%v, want 0/0/false", od, rd, dropped)
	}
	if !slices.Equal(pairs2(c.Pairs()), pairs2(before)) {
		t.Error("2->1 changed R")
	}
	if od, rd, dropped := c.RemoveEdge("a", "b"); !dropped || od != 1 || rd != 0 {
		t.Errorf("1->0: od/rd/dropped = %d/%d/%v, want 1/0/true", od, rd, dropped)
	}
	if len(c.Pairs()) != 0 {
		t.Error("edge gone but R not empty")
	}
}

// TestMonotoneDelta 不变量3：加不缩、删不增，删边减量恰为 OD-RD。
func TestMonotoneDelta(t *testing.T) {
	c := New()
	rng := rand.New(rand.NewPCG(9, 9))
	names := []string{"a", "b", "c", "d"}
	prev := 0
	for i := 0; i < 400; i++ {
		u, v := names[rng.IntN(4)], names[rng.IntN(4)]
		if c.HasEdge(u, v) && rng.IntN(2) == 0 {
			od, rd, dropped := c.RemoveEdge(u, v)
			n := len(c.Pairs())
			if n > prev || dropped && prev-n != od-rd || !dropped && n != prev {
				t.Fatalf("op %d: |R| %d->%d od=%d rd=%d dropped=%v", i, prev, n, od, rd, dropped)
			}
			prev = n
		} else {
			c.AddEdge(u, v)
			if n := len(c.Pairs()); n < prev {
				t.Fatalf("op %d: add shrank R %d->%d", i, prev, n)
			} else {
				prev = n
			}
		}
	}
}

// TestCheckedLocal 复杂度：m 条互不相连的边之外的小连通块增删，
// 检查的可达对个数不随 m 增长（索引定位，非整表扫描）。
func TestCheckedLocal(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := New()
		for i := 0; i < m; i++ {
			c.AddEdge(fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i))
		}
		c.AddEdge("p", "q")
		c.AddEdge("q", "r")
		c.AddEdge("r", "s")
		if c.checked > 4 {
			t.Fatalf("m=%d: add checked %d pairs, want <= 4", m, c.checked)
		}
		c.RemoveEdge("r", "s")
		if c.checked > 8 {
			t.Fatalf("m=%d: remove checked %d pairs, want <= 8", m, c.checked)
		}
	}
}
