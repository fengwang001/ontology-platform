package reachability

import (
	"testing"
)

// ---- 朴素参考实现（独立于产品代码的简单 DFS） ----

type naiveGraph struct {
	edges map[Pair]uint64
}

func newNaive() *naiveGraph {
	return &naiveGraph{edges: map[Pair]uint64{}}
}

func (n *naiveGraph) adj() map[string]map[string]struct{} {
	adj := map[string]map[string]struct{}{}
	for e, m := range n.edges {
		if m == 0 {
			continue
		}
		ns, ok := adj[e.From]
		if !ok {
			ns = map[string]struct{}{}
			adj[e.From] = ns
		}
		ns[e.To] = struct{}{}
	}
	return adj
}

// closure 以朴素深度优先遍历计算长度至少为 1 的全量可达点对。
func (n *naiveGraph) closure() []Pair {
	adj := n.adj()
	var pairs []Pair
	for u := range adj {
		seen := map[string]struct{}{}
		var walk func(cur string)
		walk = func(cur string) {
			for v := range adj[cur] {
				if _, ok := seen[v]; ok {
					continue
				}
				seen[v] = struct{}{} // u 仅在经环回到自身时才进入 seen
				walk(v)
			}
		}
		walk(u)
		for v := range seen {
			pairs = append(pairs, Pair{u, v})
		}
	}
	sortPairs(pairs)
	return pairs
}

func pairsOf(g *Graph) []Pair {
	ps := g.ReachablePairs()
	if ps == nil {
		return []Pair{}
	}
	return ps
}

func assertPairsEqual(t *testing.T, got, want []Pair, ctx string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: pair count = %d, want %d\ngot:  %v\nwant: %v", ctx, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: pair[%d] = %v, want %v\ngot:  %v\nwant: %v",
				ctx, i, got[i], want[i], got, want)
		}
	}
}

func TestEmptyGraph(t *testing.T) {
	g := New()
	if got := pairsOf(g); len(got) != 0 {
		t.Fatalf("empty graph has pairs %v", got)
	}
	ok, err := g.Reachable("a", "b")
	if ok || err != nil {
		t.Fatalf("empty graph reachable = %v, %v", ok, err)
	}
}

func TestSimplePathAndSelfLoop(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "b")
	mustAdd(t, g, "b", "c")

	want := []Pair{{"a", "b"}, {"a", "c"}, {"b", "c"}}
	assertPairsEqual(t, pairsOf(g), want, "path a->b->c")

	if ok, _ := g.Reachable("a", "c"); !ok {
		t.Fatal("a should reach c")
	}
	if ok, _ := g.Reachable("c", "a"); ok {
		t.Fatal("c must not reach a")
	}
	if ok, _ := g.Reachable("a", "a"); ok {
		t.Fatal("a must not reach itself without a cycle")
	}

	// 自环：节点可达自身。
	mustAdd(t, g, "a", "a")
	if ok, _ := g.Reachable("a", "a"); !ok {
		t.Fatal("a should reach itself after self-loop")
	}
	// 删除自环后自身不再可达（a 仍可到 b、c，但没有回到 a 的环）。
	mustRemove(t, g, "a", "a")
	if ok, _ := g.Reachable("a", "a"); ok {
		t.Fatal("a self-reachability must disappear with the self-loop")
	}
}
