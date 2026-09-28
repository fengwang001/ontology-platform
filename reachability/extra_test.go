package reachability

import "testing"

// TestRemoveEdgeN 批量减量：n 小于重数只降重数；n 等于重数才删边；
// n 大于重数拒绝且状态不变。
func TestRemoveEdgeN(t *testing.T) {
	g := New()
	mustAddN(t, g, "a", "b", 3)
	mustAdd(t, g, "b", "c")
	mustAdd(t, g, "c", "a")

	// 减 1：重数 3 -> 2，结构不变。
	before := pairsOf(g)
	res, err := g.RemoveEdgeN("a", "b", 1)
	if err != nil {
		t.Fatalf("remove 1: %v", err)
	}
	if res.StructureChanged || res.MultiplicityAfter != 2 {
		t.Fatalf("got structure=%v after=%d", res.StructureChanged, res.MultiplicityAfter)
	}
	assertPairsEqual(t, pairsOf(g), before, "decrement 3->2")

	// 试图减 5（超过重数 2）：拒绝 edge_not_found，状态不变。
	_, err = g.RemoveEdgeN("a", "b", 5)
	assertErrKind(t, err, KindEdgeNotFound, ErrEdgeNotFound, "removeN 5 of 2")
	assertPairsEqual(t, pairsOf(g), before, "after over-removal rejection")
	if res2 := g.ReachablePairs(); len(res2) != len(before) {
		t.Fatal("indices changed after rejected removal")
	}

	// 减 2：重数归零，环断裂。
	res, err = g.RemoveEdgeN("a", "b", 2)
	if err != nil || !res.StructureChanged {
		t.Fatalf("remove 2: err=%v structure=%v", err, res.StructureChanged)
	}
	assertPairsEqual(t, pairsOf(g),
		[]Pair{{"b", "a"}, {"b", "c"}, {"c", "a"}}, "after removeN 2")
}

// TestDeleteBridgeEdge 删除不在环上的桥边：只有经过该边的点对消失，
// 其余点对不受影响（候选集合精确，恢复集合为空）。
func TestDeleteBridgeEdge(t *testing.T) {
	g := New()
	// a->b->c 与 x->y 两个互不相连的链。
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"x", "y"}} {
		mustAdd(t, g, e[0], e[1])
	}
	res, err := g.RemoveEdge("b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RestoredPairs) != 0 {
		t.Fatalf("bridge removal should restore nothing, got %v", res.RestoredPairs)
	}
	want := []Pair{{"a", "b"}, {"x", "y"}}
	assertPairsEqual(t, pairsOf(g), want, "after bridge deletion")

	// 全部边删光后可达集合为空。
	for _, e := range [][2]string{{"a", "b"}, {"x", "y"}} {
		mustRemove(t, g, e[0], e[1])
	}
	if got := pairsOf(g); len(got) != 0 {
		t.Fatalf("expected empty reachability, got %v", got)
	}
}

// TestDeleteSelfLoop 自环的增删只影响自身可达性。
func TestDeleteSelfLoop(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "b")
	mustAdd(t, g, "b", "a")
	// 已有 2-环使 a、b 自可达；再叠加自环后删除自环，自可达应保留。
	mustAdd(t, g, "a", "a")
	mustRemove(t, g, "a", "a")
	for v, want := range map[string]bool{"a": true, "b": true} {
		got, _ := g.Reachable(v, v)
		if got != want {
			t.Fatalf("%s self-reachable = %v, want %v", v, got, want)
		}
	}

	// 断开 2-环：a->b 保留，b->a 删除；自可达全部消失，a->b 仍在。
	mustRemove(t, g, "b", "a")
	if ok, _ := g.Reachable("a", "a"); ok {
		t.Fatal("a must not reach itself after cycle broken")
	}
	if ok, _ := g.Reachable("b", "b"); ok {
		t.Fatal("b must not reach itself after cycle broken")
	}
	if ok, _ := g.Reachable("a", "b"); !ok {
		t.Fatal("a->b should survive")
	}
}

// TestSnapshotIsolation 已取得的快照在后续增删后保持不变。
func TestSnapshotIsolation(t *testing.T) {
	g := New()
	mustAdd(t, g, "a", "b")
	mustAdd(t, g, "b", "c")
	snap := g.Snapshot()
	want := snap.Pairs()

	mustAdd(t, g, "c", "d")
	mustAdd(t, g, "d", "a")
	mustRemove(t, g, "a", "b")

	assertPairsEqual(t, snap.Pairs(), want, "old snapshot must be immutable")
	for _, p := range want {
		if !snap.Reachable(p.From, p.To) {
			t.Fatalf("old snapshot lost pair %v", p)
		}
	}
	if snap.Reachable("a", "d") {
		t.Fatal("old snapshot must not observe pairs added later")
	}
}
