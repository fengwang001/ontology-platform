package closure

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/graph"
)

// sameAsNaive 报告 c 的 R 是否与对 g 朴素 BFS 的结果逐对相同。
func sameAsNaive(c *Closure, g *graph.Graph) bool {
	want := Naive(g)
	got := c.Pairs()
	if len(got) != len(want) {
		return false
	}
	for _, p := range got {
		if !want[p] {
			return false
		}
	}
	return true
}

// TestCheckedBounded：m 条互不相连的边之外的小连通块里加/删一条边，
// 检查过的可达对个数不随 m 增长（凭按节点索引定位，而非整表扫描）。
func TestCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g := graph.New()
		c := New(g)
		for i := 0; i < m; i++ {
			u, v := fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)
			g.Add(u, v)
			c.AddEdge(u, v)
		}
		if len(c.Pairs()) != m {
			t.Fatalf("m=%d: |R|=%d, want %d", m, len(c.Pairs()), m)
		}
		g.Add("p", "q")
		c.AddEdge("p", "q")
		g.Add("q", "s")
		c.AddEdge("q", "s")
		if c.checked > 8 {
			t.Fatalf("m=%d: 加边检查了 %d 对，随 m 增长", m, c.checked)
		}
		g.Remove("q", "s")
		od, re := c.RemoveEdge("q", "s")
		if od != 2 || re != 0 {
			t.Fatalf("m=%d: 过删/再推=%d/%d, want 2/0", m, od, re)
		}
		if c.checked > 8 {
			t.Fatalf("m=%d: 删边检查了 %d 对，随 m 增长", m, c.checked)
		}
		if len(c.Pairs()) != m+1 {
			t.Fatalf("m=%d: 删后 |R|=%d, want %d", m, len(c.Pairs()), m+1)
		}
	}
}

// TestRandomVsNaive：随机增删序列的每一步之后，R 与朴素 BFS 逐对相同。
func TestRandomVsNaive(t *testing.T) {
	for _, tc := range []struct {
		seed       int64
		nodes, ops int
	}{{1, 4, 200}, {2, 8, 500}, {3, 12, 1000}} {
		rng := rand.New(rand.NewSource(tc.seed))
		g := graph.New()
		c := New(g)
		name := func(i int) string { return fmt.Sprintf("n%d", i) }
		for i := 0; i < tc.ops; i++ {
			u, v := name(rng.Intn(tc.nodes)), name(rng.Intn(tc.nodes))
			if rng.Intn(2) == 0 {
				if g.Add(u, v) {
					c.AddEdge(u, v)
				}
			} else if g.Has(u, v) {
				if g.Remove(u, v) {
					c.RemoveEdge(u, v)
				}
			}
			if !sameAsNaive(c, g) {
				t.Fatalf("tc=%+v 步%d 后与朴素BFS不一致", tc, i)
			}
		}
	}
}

// TestMonotone：加边不让 R 变小，删边不让 R 变大，且删边净减 = 过删 − 再推。
func TestMonotone(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	g := graph.New()
	c := New(g)
	prev := 0
	for i := 0; i < 500; i++ {
		u, v := fmt.Sprintf("n%d", rng.Intn(8)), fmt.Sprintf("n%d", rng.Intn(8))
		if rng.Intn(2) == 0 {
			if g.Add(u, v) {
				c.AddEdge(u, v)
			}
			if len(c.Pairs()) < prev {
				t.Fatalf("步%d 加边后 R 缩小", i)
			}
		} else if g.Has(u, v) {
			od, re := 0, 0
			if g.Remove(u, v) {
				od, re = c.RemoveEdge(u, v)
			}
			if len(c.Pairs()) > prev {
				t.Fatalf("步%d 删边后 R 增大", i)
			}
			if prev-len(c.Pairs()) != od-re {
				t.Fatalf("步%d 净减=%d, 过删-再推=%d", i, prev-len(c.Pairs()), od-re)
			}
		}
		prev = len(c.Pairs())
	}
}
