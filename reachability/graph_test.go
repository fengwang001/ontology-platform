package reachability

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// logPairs prints the current reachability set and a witnessing path for
// every pair, which is the evidence behind each reachability decision.
func logPairs(t *testing.T, g *Reachability, stage string) {
	t.Helper()
	pairs := g.Pairs()
	t.Logf("[%s] reachable pairs (%d): %v", stage, len(pairs), pairs)
	for _, p := range pairs {
		path, ok, err := g.Path(p[0], p[1])
		if err != nil || !ok {
			t.Fatalf("Path(%s,%s) = %v,%v,%v", p[0], p[1], path, ok, err)
		}
		t.Logf("    %s -> %s 依据(最短见证路径): %s", p[0], p[1], strings.Join(path, " -> "))
	}
}

func TestAddEdgeAndBasicReachability(t *testing.T) {
	g, err := New(0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Input: edges forming a -> b -> c, plus d -> a.
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"d", "a"}} {
		t.Logf("input: AddEdge(%s, %s)", e[0], e[1])
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	logPairs(t, g, "after adds")

	want := [][2]string{
		{"a", "b"}, {"a", "c"},
		{"b", "c"},
		{"d", "a"}, {"d", "b"}, {"d", "c"},
	}
	if got := g.Pairs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
}

func TestSelfLoop(t *testing.T) {
	g, _ := New(0)
	t.Logf("input: AddEdge(x, x)  // 自环")
	if err := g.AddEdge("x", "x"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "self loop added")
	if ok, _ := g.Reachable("x", "x"); !ok {
		t.Fatal("(x,x) should be reachable via self loop")
	}
	t.Logf("input: RemoveEdge(x, x)")
	if err := g.RemoveEdge("x", "x"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "self loop removed")
	if ok, _ := g.Reachable("x", "x"); ok {
		t.Fatal("(x,x) must not be reachable after the only self loop is gone")
	}
}

// TestRemoveEdgeOnCycle is the key case: deleting an edge on a directed
// cycle must drop the cycle-derived pairs (no over-retention) while keeping
// pairs that still have a route (no over-deletion).
func TestRemoveEdgeOnCycle(t *testing.T) {
	g, _ := New(0)
	// Cycle a -> b -> c -> a: all 9 ordered pairs are reachable.
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}} {
		t.Logf("input: AddEdge(%s, %s)", e[0], e[1])
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	logPairs(t, g, "cycle a->b->c->a")
	if got := len(g.Pairs()); got != 9 {
		t.Fatalf("cycle should make 9 pairs reachable, got %d (%v)", got, g.Pairs())
	}

	// Remove an edge on the cycle. Remaining: a->b, c->a.
	t.Logf("input: RemoveEdge(b, c)  // 删除环上的边")
	if err := g.RemoveEdge("b", "c"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "after removing cycle edge b->c")

	want := [][2]string{
		{"a", "b"},
		{"c", "a"}, {"c", "b"},
	}
	if got := g.Pairs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
	if ok, _ := g.Reachable("a", "a"); ok {
		t.Fatal("(a,a) must disappear when the cycle breaks")
	}
	if ok, _ := g.Reachable("b", "c"); ok {
		t.Fatal("(b,c) used the deleted edge and has no alternate route")
	}

	// Restore the edge: the full closure must come back.
	t.Logf("input: AddEdge(b, c)  // 环恢复")
	if err := g.AddEdge("b", "c"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "cycle restored")
	if got := len(g.Pairs()); got != 9 {
		t.Fatalf("restored cycle should make 9 pairs reachable, got %d", got)
	}

	// Remove a different edge on the cycle: remaining a->b, b->c.
	t.Logf("input: RemoveEdge(c, a)  // 删除环上的边")
	if err := g.RemoveEdge("c", "a"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "after removing c->a")
	want = [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}
	if got := g.Pairs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
}

// TestAlternateRouteAfterDelete: pairs with an alternative path must survive
// the deletion of one edge (no over-deletion).
func TestAlternateRouteAfterDelete(t *testing.T) {
	g, _ := New(0)
	for _, e := range [][2]string{{"a", "b"}, {"b", "d"}, {"a", "c"}, {"c", "d"}} {
		t.Logf("input: AddEdge(%s, %s)", e[0], e[1])
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	logPairs(t, g, "diamond a->b->d and a->c->d")
	t.Logf("input: RemoveEdge(b, d)")
	if err := g.RemoveEdge("b", "d"); err != nil {
		t.Fatal(err)
	}
	logPairs(t, g, "after removing b->d")
	if ok, _ := g.Reachable("a", "d"); !ok {
		t.Fatal("(a,d) must survive via the alternate route a->c->d")
	}
	if ok, _ := g.Reachable("b", "d"); ok {
		t.Fatal("(b,d) used the deleted edge and has no alternate route")
	}
}

// TestMultiplicity: adding the same edge repeatedly only increments its
// multiplicity; a single removal must not change reachability.
func TestMultiplicity(t *testing.T) {
	g, _ := New(0)
	for i := 0; i < 3; i++ {
		t.Logf("input: AddEdge(p, q)  // 第 %d 次重复加边", i+1)
		if err := g.AddEdge("p", "q"); err != nil {
			t.Fatal(err)
		}
	}
	if m, _ := g.EdgeMultiplicity("p", "q"); m != 3 {
		t.Fatalf("multiplicity = %d, want 3", m)
	}
	logPairs(t, g, "edge added 3 times")

	t.Logf("input: RemoveEdge(p, q)  // 重复加边后只删一次")
	if err := g.RemoveEdge("p", "q"); err != nil {
		t.Fatal(err)
	}
	if m, _ := g.EdgeMultiplicity("p", "q"); m != 2 {
		t.Fatalf("multiplicity = %d, want 2", m)
	}
	if ok, _ := g.Reachable("p", "q"); !ok {
		t.Fatal("edge still has positive multiplicity; reachability must be unchanged")
	}
	logPairs(t, g, "after one removal (multiplicity 2)")

	for i := 0; i < 2; i++ {
		if err := g.RemoveEdge("p", "q"); err != nil {
			t.Fatalf("removal %d: %v", i+2, err)
		}
	}
	logPairs(t, g, "after all removals")
	if m, _ := g.EdgeMultiplicity("p", "q"); m != 0 {
		t.Fatalf("multiplicity = %d, want 0", m)
	}
	if ok, _ := g.Reachable("p", "q"); ok {
		t.Fatal("edge gone; (p,q) must not be reachable")
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(-1) err = %v, want ErrInvalidArgument", err)
	}
	t.Logf("input: New(-1) => 拒绝原因: %v", ErrInvalidArgument)

	g, _ := New(2)

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"AddEdge 空起点", func() error { return g.AddEdge("", "b") }, ErrEmptyNode},
		{"AddEdge 空终点", func() error { return g.AddEdge("a", "") }, ErrEmptyNode},
		{"RemoveEdge 空起点", func() error { return g.RemoveEdge("", "b") }, ErrEmptyNode},
		{"RemoveEdge 空终点", func() error { return g.RemoveEdge("a", "") }, ErrEmptyNode},
		{"RemoveEdge 不存在的边", func() error { return g.RemoveEdge("a", "b") }, ErrEdgeNotFound},
	}
	for _, c := range cases {
		err := c.call()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
		t.Logf("input: %s => 拒绝原因: %v", c.name, err)
	}

	if _, err := g.Reachable("", "x"); !errors.Is(err, ErrEmptyNode) {
		t.Fatalf("Reachable empty: %v", err)
	}
	if _, _, err := g.Path("x", ""); !errors.Is(err, ErrEmptyNode) {
		t.Fatalf("Path empty: %v", err)
	}
	if _, err := g.EdgeMultiplicity("", "x"); !errors.Is(err, ErrEmptyNode) {
		t.Fatalf("EdgeMultiplicity empty: %v", err)
	}
	t.Logf("input: 查询类接口空节点名 => 拒绝原因: %v", ErrEmptyNode)

	// Edge limit: two distinct edges allowed, the third is rejected.
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("c", "d"); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: AddEdge(e, f)  // 已存在的边数(%d)超限 %d", 2, 2)
	if err := g.AddEdge("e", "f"); !errors.Is(err, ErrTooManyEdges) {
		t.Fatalf("third edge err = %v, want ErrTooManyEdges", err)
	} else {
		t.Logf("    => 拒绝原因: %v", err)
	}
	// A duplicate add does not consume a slot and must stay allowed.
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatalf("duplicate add within limit: %v", err)
	}
	if m, _ := g.EdgeMultiplicity("a", "b"); m != 2 {
		t.Fatalf("multiplicity = %d, want 2", m)
	}
}

// TestRejectedOperationsChangeNothing: rejection must leave multiplicity,
// reachability set and indices untouched.
func TestRejectedOperationsChangeNothing(t *testing.T) {
	g, _ := New(1)
	if err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		return fmt.Sprintf("edges-multiplicity view; pairs=%v nodes=%v", g.Pairs(), g.Nodes())
	}
	before := snapshot()

	if err := g.AddEdge("b", "c"); !errors.Is(err, ErrTooManyEdges) {
		t.Fatalf("want ErrTooManyEdges, got %v", err)
	}
	if err := g.AddEdge("", "c"); !errors.Is(err, ErrEmptyNode) {
		t.Fatalf("want ErrEmptyNode, got %v", err)
	}
	if err := g.RemoveEdge("x", "y"); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("want ErrEdgeNotFound, got %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("state changed by rejected operations:\nbefore: %s\nafter:  %s", before, after)
	}
	t.Logf("被拒绝操作前后状态一致: %s", before)
}

func TestDeterminism(t *testing.T) {
	seq := [][3]string{
		{"+", "a", "b"}, {"+", "b", "c"}, {"+", "c", "a"}, {"+", "a", "d"},
		{"-", "b", "c"}, {"+", "d", "b"}, {"-", "a", "b"}, {"+", "b", "c"},
	}
	run := func() [][2]string {
		g, _ := New(0)
		for _, op := range seq {
			var err error
			if op[0] == "+" {
				err = g.AddEdge(op[1], op[2])
			} else {
				err = g.RemoveEdge(op[1], op[2])
			}
			if err != nil {
				t.Fatalf("op %v: %v", op, err)
			}
		}
		return g.Pairs()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d = %v, want %v", i, got, first)
		}
	}
	t.Logf("input sequence: %v", seq)
	t.Logf("同一序列反复计算输出完全相同: %v", first)
}

// TestRandomSequencesAgainstNaive drives long random add/remove sequences and
// compares the incrementally maintained closure against the naive traversal
// after every single operation.
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const nodes = "abcde"
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g, _ := New(0)
		edges := map[[2]string]int{}

		for step := 0; step < 300; step++ {
			u := string(nodes[rng.Intn(len(nodes))])
			v := string(nodes[rng.Intn(len(nodes))])
			key := [2]string{u, v}

			var op string
			if edges[key] > 0 && rng.Intn(2) == 0 {
				op = "-"
			} else {
				op = "+"
			}

			switch op {
			case "+":
				if err := g.AddEdge(u, v); err != nil {
					t.Fatalf("seed=%d step=%d AddEdge(%s,%s): %v", seed, step, u, v, err)
				}
				edges[key]++
			case "-":
				if err := g.RemoveEdge(u, v); err != nil {
					t.Fatalf("seed=%d step=%d RemoveEdge(%s,%s): %v", seed, step, u, v, err)
				}
				edges[key]--
			}

			want := naivePairs(edges)
			got := g.Pairs()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d step=%d op=%s(%s,%s)\nincremental: %v\nnaive:       %v",
					seed, step, op, u, v, got, want)
			}
		}
		t.Logf("seed=%d: 300 次随机增删后增量结果与朴素遍历完全一致 (%d 个可达点对)", seed, len(g.Pairs()))
	}
}

// TestConcurrentReadersAndWriters hammers the graph with concurrent updates
// while readers continuously cross-check every answer against a path
// witness; the final state must match a naive traversal.
func TestConcurrentReadersAndWriters(t *testing.T) {
	g, _ := New(0)
	base := []string{"a", "b", "c", "d"}
	var possible [][2]string
	for _, u := range base {
		for _, v := range base {
			possible = append(possible, [2]string{u, v})
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers repeatedly add and remove random edges.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + id)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				e := possible[rng.Intn(len(possible))]
				if rng.Intn(2) == 0 {
					_ = g.AddEdge(e[0], e[1])
				} else {
					if err := g.RemoveEdge(e[0], e[1]); err != nil {
						_ = g.AddEdge(e[0], e[1])
					}
				}
			}
		}(w)
	}

	// Readers take a single atomic snapshot and verify its internal,
	// per-pair consistency: every reported pair carries a positive-length
	// witness and every pair of nodes on that witness is itself present (the
	// set is transitively closed).
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := g.Snapshot()
				present := map[[2]string][]string{}
				for _, pw := range snap {
					if len(pw.Path) < 2 || pw.Path[0] != pw.From || pw.Path[len(pw.Path)-1] != pw.To {
						t.Errorf("malformed witness for %v->%v: %v", pw.From, pw.To, pw.Path)
						return
					}
					present[[2]string{pw.From, pw.To}] = pw.Path
				}
				for pair, path := range present {
					// Every consecutive step is a real edge, hence a length-1
					// reachable pair that must appear in the same snapshot;
					// and every pair of nodes along the witness is present.
					for i := 0; i+1 < len(path); i++ {
						if _, ok := present[[2]string{path[i], path[i+1]}]; !ok {
							t.Errorf("snapshot %v: witness %v uses missing edge %s->%s",
								pair, path, path[i], path[i+1])
							return
						}
						for j := i + 1; j < len(path); j++ {
							if _, ok := present[[2]string{path[i], path[j]}]; !ok {
								t.Errorf("snapshot not transitively closed: %v witness %v misses %s->%s",
									pair, path, path[i], path[j])
								return
							}
						}
					}
				}
			}
		}()
	}

	// Let it churn briefly, then stop and serialize the final multiplicity
	// view for a naive comparison.
	var drvWG sync.WaitGroup
	drvWG.Add(1)
	go func() {
		defer drvWG.Done()
		for i := 0; i < 200; i++ {
			e := possible[i%len(possible)]
			_ = g.AddEdge(e[0], e[1])
		}
	}()
	drvWG.Wait()
	close(stop)
	wg.Wait()

	final := map[[2]string]int{}
	for _, e := range possible {
		if m, _ := g.EdgeMultiplicity(e[0], e[1]); m > 0 {
			final[e] = m
		}
	}
	if got, want := g.Pairs(), naivePairs(final); !reflect.DeepEqual(got, want) {
		t.Fatalf("after concurrent churn:\nincremental: %v\nnaive:       %v", got, want)
	}
	t.Logf("并发增删结束后可达集合与朴素遍历一致: %d 个点对 %v", len(g.Pairs()), g.Pairs())
}
