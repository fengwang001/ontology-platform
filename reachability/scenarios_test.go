package reachability

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestDeleteEdgeOnCycle 覆盖“删环上的边”：先移除可能受影响点对，
// 再把仍可达的点对重新推导加回，最终与朴素遍历一致。
func TestDeleteEdgeOnCycle(t *testing.T) {
	g := New()
	// 在环 a->b->c->a 之外，再挂一个入口 p 和出口 q：
	// p->a, a->b, b->c, c->a, b->q
	edges := [][2]string{
		{"p", "a"}, {"a", "b"}, {"b", "c"}, {"c", "a"}, {"b", "q"},
	}
	for _, e := range edges {
		mustAdd(t, g, e[0], e[1])
	}

	// 删环边前：强连通的 a,b,c 两两可达（含自身），p 可到环上所有点及 q。
	cases := []struct {
		from, to string
		want     bool
	}{
		{"a", "a", true}, {"b", "b", true}, {"c", "c", true},
		{"a", "c", true}, {"c", "b", true},
		{"p", "a", true}, {"p", "c", true}, {"p", "p", false},
		{"p", "q", true}, {"q", "q", false}, {"c", "q", true},
	}
	for _, c := range cases {
		got, _ := g.Reachable(c.from, c.to)
		if got != c.want {
			t.Fatalf("before delete: %s->%s = %v, want %v", c.from, c.to, got, c.want)
		}
	}

	// 删除环边 c->a：环断裂，a,b,c 自可达消失；但 a->b->c、a->q 仍成立。
	res, err := g.RemoveEdge("c", "a")
	if err != nil {
		t.Fatalf("remove c->a: %v", err)
	}
	// 必须先移除过受影响点对，且其中一部分被恢复（a->c 等）。
	if len(res.RemovedPairs) == 0 {
		t.Fatal("expected removed pairs to be reported")
	}
	if len(res.RestoredPairs) == 0 {
		t.Fatal("expected some pairs restored via alternate paths")
	}
	// 被恢复的点对必须给出删边后仍成立的见证路径。
	for _, w := range res.RestoredPairs {
		if !pathExists(w.From, w.To, w.Path, g) {
			t.Fatalf("restored witness path invalid in post-delete graph: %v", w.Path)
		}
	}

	after := map[Pair]bool{
		{"p", "a"}: true, {"p", "b"}: true, {"p", "c"}: true, {"p", "q"}: true,
		{"a", "b"}: true, {"a", "c"}: true, {"a", "q"}: true,
		{"b", "c"}: true, {"b", "q"}: true,
	}
	got := pairsOf(g)
	if len(got) != len(after) {
		t.Fatalf("after deleting cycle edge: %d pairs %v, want %d", len(got), got, len(after))
	}
	for _, p := range got {
		if !after[p] {
			t.Fatalf("unexpected pair after delete: %v (pairs=%v)", p, got)
		}
	}
	for p := range after {
		if ok, _ := g.Reachable(p.From, p.To); !ok {
			t.Fatalf("pair %v should remain reachable after delete", p)
		}
	}
	// 自可达必须随环一起消失。
	for _, v := range []string{"a", "b", "c"} {
		if ok, _ := g.Reachable(v, v); ok {
			t.Fatalf("%s should no longer reach itself after breaking the cycle", v)
		}
	}
}

// pathExists 校验 path 是删边后图中 from->to 的合法有向路径。
func pathExists(from, to string, path []string, g *Graph) bool {
	if len(path) < 2 || path[0] != from || path[len(path)-1] != to {
		return false
	}
	for i := 0; i+1 < len(path); i++ {
		if _, ok := g.out[path[i]][path[i+1]]; !ok {
			return false
		}
	}
	return true
}

// TestRepeatedAddThenSingleDelete：重复加边只改重数，删一次不删边，
// 直到重数归零边才消失。
func TestRepeatedAddThenSingleDelete(t *testing.T) {
	g := New()
	mustAddN(t, g, "a", "b", 3) // 重数 3
	mustAdd(t, g, "b", "c")
	mustAdd(t, g, "c", "a")

	// 环已存在；删一次只把 a->b 重数从 3 降到 2，结构与可达集合不变。
	before := pairsOf(g)
	res, err := g.RemoveEdge("a", "b")
	if err != nil {
		t.Fatalf("remove once: %v", err)
	}
	if res.StructureChanged {
		t.Fatal("structure must not change while multiplicity stays positive")
	}
	if len(res.RemovedPairs) != 0 || len(res.RestoredPairs) != 0 {
		t.Fatal("no reachability changes expected for multiplicity-only decrement")
	}
	assertPairsEqual(t, pairsOf(g), before, "after one decrement of multiplicity-3 edge")

	// 再删两次，重数归零，环断裂。此时只剩 b->c 与 c->a：
	// a 没有出边；b 可达 c、a；c 可达 a。
	mustRemove(t, g, "a", "b")
	mustRemove(t, g, "a", "b")
	assertPairsEqual(t, pairsOf(g),
		[]Pair{{"b", "a"}, {"b", "c"}, {"c", "a"}}, "after cycle broken")
	if ok, _ := g.Reachable("a", "a"); ok {
		t.Fatal("cycle reachability must be gone only after multiplicity reaches zero")
	}
	if ok, _ := g.Reachable("b", "a"); !ok {
		t.Fatal("b->c->a should still be reachable")
	}

	// 边已不存在，继续删必须报 edge_not_found。
	_, err = g.RemoveEdge("a", "b")
	assertErrKind(t, err, KindEdgeNotFound, ErrEdgeNotFound, "remove missing edge")
}

// TestMultiplicityOverflow 验证“存在的边数超限”被拒绝且状态不变。
func TestMultiplicityOverflow(t *testing.T) {
	g := New()
	if _, err := g.AddEdgeN("a", "b", MaxEdgeMultiplicity); err != nil {
		t.Fatalf("seed at max: %v", err)
	}
	_, err := g.AddEdge("a", "b")
	assertErrKind(t, err, KindMultiplicityOverflow, ErrMultiplicityOverflow, "add over max")

	// 一次性加 n 使总和超限也拒绝。
	_, err = g.AddEdgeN("a", "b", 1)
	assertErrKind(t, err, KindMultiplicityOverflow, ErrMultiplicityOverflow, "addN over max")

	// 状态不变：重数仍为上限，可达集合仍只有 a->b。
	assertPairsEqual(t, pairsOf(g), []Pair{{"a", "b"}}, "after overflow rejection")

	// 大数加法溢出场景：old+n 回绕但实际超限。
	g2 := New()
	mustAddN(t, g2, "x", "y", MaxEdgeMultiplicity-5)
	_, err = g2.AddEdgeN("x", "y", 6)
	assertErrKind(t, err, KindMultiplicityOverflow, ErrMultiplicityOverflow, "addN wraps past max")
}

// TestInvalidInputs 覆盖各类非法输入，并确认被拒绝操作不改变任何状态。
func TestInvalidInputs(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "b")

	type tc struct {
		name     string
		kind     ErrorKind
		sentinel error
		call     func() error
	}
	mustAddN(t, g, "m", "n", 2) // 为 removeN 超限场景预置重数
	cases := []tc{
		{"add empty from", KindEmptyNodeName, ErrEmptyNodeName,
			func() error { _, e := g.AddEdge("", "b"); return e }},
		{"add empty to", KindEmptyNodeName, ErrEmptyNodeName,
			func() error { _, e := g.AddEdge("a", ""); return e }},
		{"add both empty", KindEmptyNodeName, ErrEmptyNodeName,
			func() error { _, e := g.AddEdge("", ""); return e }},
		{"addN zero count", KindInvalidArgument, ErrInvalidArgument,
			func() error { _, e := g.AddEdgeN("a", "b", 0); return e }},
		{"removeN zero count", KindInvalidArgument, ErrInvalidArgument,
			func() error { _, e := g.RemoveEdgeN("a", "b", 0); return e }},
		{"remove empty name", KindEmptyNodeName, ErrEmptyNodeName,
			func() error { _, e := g.RemoveEdge("", "b"); return e }},
		{"remove missing edge", KindEdgeNotFound, ErrEdgeNotFound,
			func() error { _, e := g.RemoveEdge("a", "z"); return e }},
		{"removeN more than multiplicity", KindEdgeNotFound, ErrEdgeNotFound,
			func() error { _, e := g.RemoveEdgeN("m", "n", 3); return e }},
	}

	for _, c := range cases {
		before := pairsOf(g)
		err := c.call()
		assertErrKind(t, err, c.kind, c.sentinel, c.name)
		assertPairsEqual(t, pairsOf(g), before, "state after rejected: "+c.name)
	}

	// Reachable 空名同样拒绝。
	_, err := g.Reachable("", "b")
	assertErrKind(t, err, KindEmptyNodeName, ErrEmptyNodeName, "reachable empty name")
	if !errors.Is(err, ErrEmptyNodeName) {
		t.Fatal("errors.Is mismatch")
	}
}

// TestRandomOpsMatchesNaive 随机并发无关地顺序执行增删，每步都与朴素遍历比对。
func TestRandomOpsMatchesNaive(t *testing.T) {
	// 为使可达集合丰富，节点集很小。
	nodes := []string{"a", "b", "c", "d"}
	rng := rand.New(rand.NewSource(275))
	g := New()
	ref := newNaive()

	for step := 0; step < 4000; step++ {
		u := nodes[rng.Intn(len(nodes))]
		v := nodes[rng.Intn(len(nodes))]
		edge := Pair{u, v}
		// 60% 加，40% 删，使图在有边/无边间反复跃迁。
		if rng.Intn(100) < 60 {
			n := uint64(1 + rng.Intn(3))
			cur := ref.edges[edge]
			if cur+n > MaxEdgeMultiplicity {
				if _, err := g.AddEdgeN(u, v, n); err == nil {
					t.Fatalf("step %d: expected overflow rejection", step)
				}
				continue
			}
			if _, err := g.AddEdgeN(u, v, n); err != nil {
				t.Fatalf("step %d add: %v", step, err)
			}
			ref.edges[edge] = cur + n
		} else {
			if _, err := g.RemoveEdge(u, v); err != nil {
				if _, exists := ref.edges[edge]; !exists {
					var opErr *OpError
					if !errors.As(err, &opErr) || opErr.Kind != KindEdgeNotFound {
						t.Fatalf("step %d: want edge_not_found, got %v", step, err)
					}
				} else {
					t.Fatalf("step %d remove: %v", step, err)
				}
				continue
			}
			cur := ref.edges[edge]
			if cur <= 1 {
				delete(ref.edges, edge)
			} else {
				ref.edges[edge] = cur - 1
			}
		}
		assertPairsEqual(t, pairsOf(g), ref.closure(), fmt.Sprintf("step %d", step))
	}
}

// TestDeterministicReplay 同一输入序列两次执行，输出（含日志）完全一致。
func TestDeterministicReplay(t *testing.T) {
	seq := [][3]int{
		{0, 1, +1}, {1, 2, +1}, {2, 0, +1}, {0, 2, +1},
		{0, 1, -1}, {2, 0, -1}, {3, 1, +1}, {1, 3, +1},
		{1, 2, -1}, {0, 2, -1}, {3, 1, -1},
	}
	run := func() (string, []Pair) {
		var buf bytes.Buffer
		g := New(WithLogger(&buf))
		nodes := []string{"a", "b", "c", "d"}
		for _, op := range seq {
			u, v := nodes[op[0]], nodes[op[1]]
			if op[2] > 0 {
				if _, err := g.AddEdge(u, v); err != nil {
					t.Fatalf("add: %v", err)
				}
			} else {
				_, _ = g.RemoveEdge(u, v) // 允许删除不存在的边并记录拒绝日志
			}
		}
		_, _ = g.Reachable("a", "d")
		_, _ = g.Reachable("d", "d")
		return buf.String(), g.ReachablePairs()
	}

	log1, pairs1 := run()
	log2, pairs2 := run()
	if log1 != log2 {
		t.Fatal("non-deterministic log output across replays")
	}
	assertPairsEqual(t, pairs1, pairs2, "replay pairs")
	if len(log1) == 0 {
		t.Fatal("expected log content")
	}
}

// TestConcurrentReadersAndWriters 并发增删后可达集合与朴素遍历一致，
// 且并发读者始终读到一致快照。
func TestConcurrentReadersAndWriters(t *testing.T) {
	nodes := []string{"a", "b", "c", "d", "e"}
	g := New()
	// 预置一些边，避免全空。
	for i := 0; i < len(nodes)-1; i++ {
		mustAdd(t, g, nodes[i], nodes[i+1])
	}

	stop := make(chan struct{})
	var readerWG, writerWG sync.WaitGroup

	// 读者：抓取快照后整段遍历必须自洽（同一版本），且点对查询与之一致。
	for r := 0; r < 8; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := g.Snapshot()
				pairs := snap.Pairs()
				for _, p := range pairs {
					if !snap.Reachable(p.From, p.To) {
						t.Errorf("inconsistent snapshot: %v listed but not queryable", p)
						return
					}
				}
				// 点对顺序稳定：重复枚举应完全相同。
				again := snap.Pairs()
				if !sortPairsEqual(pairs, again) {
					t.Errorf("snapshot enumeration unstable")
					return
				}
			}
		}()
	}

	// 写者：在固定序列上反复增删；忽略 edge_not_found。
	for w := 0; w < 4; w++ {
		writerWG.Add(1)
		go func(id int) {
			defer writerWG.Done()
			r := rand.New(rand.NewSource(int64(100 + id)))
			for i := 0; i < 500; i++ {
				u := nodes[r.Intn(len(nodes))]
				v := nodes[r.Intn(len(nodes))]
				if i%2 == 0 {
					_, _ = g.AddEdge(u, v)
				} else {
					_, _ = g.RemoveEdge(u, v)
				}
			}
		}(w)
	}

	// 先等写者全部完成，再停读者，避免读者在 stop 关闭后永久阻塞 wg。
	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	// 以图中现存边重建参考可达集合，做最终全量朴素比对。
	ref := newNaive()
	g.mu.Lock()
	for e, m := range g.edges {
		ref.edges[e] = m
	}
	g.mu.Unlock()
	assertPairsEqual(t, pairsOf(g), ref.closure(), "after concurrent writers")
}

func sortPairsEqual(a, b []Pair) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
