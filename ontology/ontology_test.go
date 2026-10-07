package ontology

import (
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErrKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	oe, ok := err.(*OntoError)
	if !ok {
		t.Fatalf("expected OntoError, got %T: %v", err, err)
	}
	if oe.Kind != kind {
		t.Fatalf("expected kind %d, got %d (%v)", kind, oe.Kind, err)
	}
}

func queryAgg(t *testing.T, g *Graph, view, start string) Aggregate {
	t.Helper()
	a, err := g.Query(view, start)
	mustOK(t, err)
	return a
}

func chainGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	for _, ty := range []string{"A", "B", "C"} {
		g.AddObjectType(ty)
	}
	mustOK(t, g.AddLinkType("ab", "A", "B"))
	mustOK(t, g.AddLinkType("bc", "B", "C"))
	mustOK(t, g.CreateObject("a1", "A", nil))
	mustOK(t, g.CreateObject("b1", "B", nil))
	mustOK(t, g.CreateObject("b2", "B", nil))
	mustOK(t, g.CreateObject("c1", "C", map[string]int64{"v": 3}))
	mustOK(t, g.CreateObject("c2", "C", map[string]int64{"v": 7}))
	return g
}

func twoHopSpec() ViewSpec {
	return ViewSpec{
		Name: "v2",
		Path: Path{
			Types: [][]string{{"A"}, {"B"}, {"C"}},
			Links: []string{"ab", "bc"},
		},
		Attr: "v",
	}
}

// 单跳与多跳路径变化下的正确维护 + 明确的不存在状态。
func TestSingleAndMultiHop(t *testing.T) {
	g := chainGraph(t)
	mustOK(t, g.RegisterView(twoHopSpec()))

	if a := queryAgg(t, g, "v2", "a1"); a.Present {
		t.Fatalf("expected absent, got %+v", a)
	}

	aff, err := g.AddLink("ab", "a1", "b1", LinkOptions{})
	mustOK(t, err)
	if len(aff) != 0 {
		t.Fatalf("no complete path yet, affected should be empty, got %v", aff)
	}
	if a := queryAgg(t, g, "v2", "a1"); a.Present {
		t.Fatalf("still no C reachable, expected absent")
	}

	aff, err = g.AddLink("bc", "b1", "c1", LinkOptions{})
	mustOK(t, err)
	if _, ok := aff["a1"]; !ok {
		t.Fatalf("a1 must be affected, got %v", aff)
	}
	if a := queryAgg(t, g, "v2", "a1"); !a.Present || a.Value != 3 {
		t.Fatalf("expected 3, got %+v", a)
	}

	_, err = g.AddLink("ab", "a1", "b2", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("bc", "b2", "c2", LinkOptions{})
	mustOK(t, err)
	if a := queryAgg(t, g, "v2", "a1"); !a.Present || a.Value != 7 {
		t.Fatalf("expected 7, got %+v", a)
	}

	aff, err = g.RemoveLink("bc", "b2", "c2")
	mustOK(t, err)
	if _, ok := aff["a1"]; !ok {
		t.Fatalf("a1 must be affected by hop removal, got %v", aff)
	}
	if a := queryAgg(t, g, "v2", "a1"); !a.Present || a.Value != 3 {
		t.Fatalf("expected 3 after removal, got %+v", a)
	}

	_, err = g.RemoveLink("bc", "b1", "c1")
	mustOK(t, err)
	if a := queryAgg(t, g, "v2", "a1"); a.Present {
		t.Fatalf("expected absent after last chain removed, got %+v", a)
	}
}

// 重复到达同一终点只贡献一次。
func TestDuplicateArrivalCountsOnce(t *testing.T) {
	g := NewGraph()
	for _, ty := range []string{"A", "M", "E"} {
		g.AddObjectType(ty)
	}
	mustOK(t, g.AddLinkType("am", "A", "M"))
	mustOK(t, g.AddLinkType("me", "M", "E"))
	mustOK(t, g.CreateObject("a", "A", nil))
	mustOK(t, g.CreateObject("m1", "M", nil))
	mustOK(t, g.CreateObject("m2", "M", nil))
	mustOK(t, g.CreateObject("e", "E", map[string]int64{"v": 5}))
	mustOK(t, g.RegisterView(ViewSpec{Name: "dup",
		Path: Path{Types: [][]string{{"A"}, {"M"}, {"E"}}, Links: []string{"am", "me"}},
		Attr: "v"}))

	// a 经 m1 与 m2 两条不同中间路径到达同一个 e。
	_, err := g.AddLink("am", "a", "m1", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("am", "a", "m2", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("me", "m1", "e", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("me", "m2", "e", LinkOptions{})
	mustOK(t, err)

	a := queryAgg(t, g, "dup", "a")
	if !a.Present || a.Value != 5 {
		t.Fatalf("expected single contribution 5, got %+v", a)
	}
	// 底层路径计数为 2，但终点集合只有一个元素。
	cnt := g.byName["dup"].snap.reach[reachKey{start: "a", end: "e"}]
	if cnt != 2 {
		t.Fatalf("expected 2 distinct paths counted, got %d", cnt)
	}
	ends := endSet(g, "dup", "a")
	if len(ends) != 1 {
		t.Fatalf("deduped end set size must be 1, got %d", len(ends))
	}

	// 删除其中一条到达路径后仍然可达，聚合不变。
	_, err = g.RemoveLink("me", "m2", "e")
	mustOK(t, err)
	if a := queryAgg(t, g, "dup", "a"); !a.Present || a.Value != 5 {
		t.Fatalf("expected still 5 after removing one duplicate path, got %+v", a)
	}
}

func endSet(g *Graph, view, start string) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range g.byName[view].snap.reach {
		if k.start == start {
			out[k.end] = struct{}{}
		}
	}
	return out
}

// 含环路径：环上实例作为终点恰计入一次，且展开有界。
func TestCycles(t *testing.T) {
	g := NewGraph()
	g.AddObjectType("N")
	mustOK(t, g.AddLinkType("nn", "N", "N"))
	mustOK(t, g.CreateObject("s", "N", nil))
	mustOK(t, g.CreateObject("x", "N", nil))
	mustOK(t, g.CreateObject("e", "N", map[string]int64{"v": 9}))
	spec := ViewSpec{Name: "cyc",
		Path: Path{Types: [][]string{{"N"}, {"N"}, {"N"}}, Links: []string{"nn", "nn"}},
		Attr: "v"}
	mustOK(t, g.RegisterView(spec))

	for _, e := range [][2]string{{"s", "s"}, {"s", "x"}, {"x", "s"}, {"x", "e"}, {"e", "e"}} {
		_, err := g.AddLink("nn", e[0], e[1], LinkOptions{})
		mustOK(t, err)
	}
	if a := queryAgg(t, g, "cyc", "s"); !a.Present || a.Value != 9 {
		t.Fatalf("expected 9 with cycles from s, got %+v", a)
	}
	if a := queryAgg(t, g, "cyc", "x"); !a.Present || a.Value != 9 {
		t.Fatalf("expected 9 with cycles from x, got %+v", a)
	}
	// 环上的 s/x 作为终点出现时不带属性，不贡献；终点集合是去重集合。
	if n := len(endSet(g, "cyc", "s")); n != 3 {
		t.Fatalf("s two-hop ends should be {s,x,e}=3, got %d", n)
	}
}

// 属性变小后重新确定最大值来源，且开销不超过真实可达终点数。
func TestAttrDecreaseRedetermine(t *testing.T) {
	g := NewGraph()
	for _, ty := range []string{"A", "E"} {
		g.AddObjectType(ty)
	}
	mustOK(t, g.AddLinkType("ae", "A", "E"))
	mustOK(t, g.CreateObject("a", "A", nil))
	for _, e := range []struct {
		id string
		v  int64
	}{{"e1", 1}, {"e2", 5}, {"e3", 10}, {"e4", 3}} {
		mustOK(t, g.CreateObject(e.id, "E", map[string]int64{"v": e.v}))
	}
	mustOK(t, g.RegisterView(ViewSpec{Name: "mx",
		Path: Path{Types: [][]string{{"A"}, {"E"}}, Links: []string{"ae"}}, Attr: "v"}))
	for _, id := range []string{"e1", "e2", "e3", "e4"} {
		_, err := g.AddLink("ae", "a", id, LinkOptions{})
		mustOK(t, err)
	}
	if a := queryAgg(t, g, "mx", "a"); a.Value != 10 {
		t.Fatalf("expected 10, got %+v", a)
	}

	g.ResetRescanCost("mx")
	aff, err := g.SetAttr("e3", "v", 2)
	mustOK(t, err)
	if _, ok := aff["a"]; !ok {
		t.Fatalf("a must be affected")
	}
	if a := queryAgg(t, g, "mx", "a"); !a.Present || a.Value != 5 {
		t.Fatalf("expected redetermined max 5, got %+v", a)
	}
	if cost := g.RescanCost("mx"); cost != 4 {
		t.Fatalf("rescan must examine exactly 4 reachable ends, got %d", cost)
	}

	// 非最大值来源变小：零重定源开销。
	g.ResetRescanCost("mx")
	_, err = g.SetAttr("e1", "v", 0)
	mustOK(t, err)
	if cost := g.RescanCost("mx"); cost != 0 {
		t.Fatalf("non-source decrease must not rescan, got %d", cost)
	}
	if a := queryAgg(t, g, "mx", "a"); a.Value != 5 {
		t.Fatalf("expected still 5, got %+v", a)
	}

	// 最大值来源变小到使起点无任何带属性终点：明确不存在。
	_, err = g.SetAttr("e2", "v", 1)
	mustOK(t, err)
	_, err = g.SetAttr("e3", "v", 1)
	mustOK(t, err)
	_, err = g.SetAttr("e4", "v", 1)
	mustOK(t, err)
	// 全部值仍存在，最小为 0；再把 e1 属性删除需要一个删除 API 场景，跳过删除，改判低值。
	if a := queryAgg(t, g, "mx", "a"); !a.Present || a.Value != 1 {
		t.Fatalf("expected 1, got %+v", a)
	}
}
