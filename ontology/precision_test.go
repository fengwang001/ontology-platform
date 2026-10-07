package ontology

import "testing"

// 受影响集合必须精确：无关起点、无关跳都不得计入。
func TestAffectedPrecision(t *testing.T) {
	g := NewGraph()
	for _, ty := range []string{"A", "B", "C"} {
		g.AddObjectType(ty)
	}
	mustOK(t, g.AddLinkType("ab", "A", "B"))
	mustOK(t, g.AddLinkType("bc", "B", "C"))
	mustOK(t, g.CreateObject("a1", "A", nil))
	mustOK(t, g.CreateObject("a2", "A", nil)) // 孤立起点，永不连通
	mustOK(t, g.CreateObject("b1", "B", nil))
	mustOK(t, g.CreateObject("c1", "C", map[string]int64{"v": 1}))
	mustOK(t, g.RegisterView(twoHopSpec()))

	// 加入不完整路径的第一跳：没有任何起点的可达终点集合变化。
	aff, err := g.AddLink("ab", "a1", "b1", LinkOptions{})
	mustOK(t, err)
	if len(aff) != 0 {
		t.Fatalf("first hop alone must affect nobody, got %v", aff)
	}

	// 第二跳补齐：只影响 a1，不影响 a2。
	aff, err = g.AddLink("bc", "b1", "c1", LinkOptions{})
	mustOK(t, err)
	if len(aff) != 1 {
		t.Fatalf("exactly one start must be affected, got %v", aff)
	}
	if _, ok := aff["a1"]; !ok {
		t.Fatalf("a1 must be the only affected start, got %v", aff)
	}

	// 属性写入不可达终点：零影响。
	aff, err = g.SetAttr("c1", "v", 42)
	mustOK(t, err)
	if _, ok := aff["a1"]; !ok {
		t.Fatalf("reachable start a1 must observe attr change, got %v", aff)
	}
	mustOK(t, g.CreateObject("c2", "C", map[string]int64{"v": 5}))
	aff, err = g.SetAttr("c2", "v", 99) // c2 对任何起点都不可达
	mustOK(t, err)
	if len(aff) != 0 {
		t.Fatalf("unreachable end change must affect nobody, got %v", aff)
	}
	if a := queryAgg(t, g, "v2", "a1"); a.Value != 42 {
		t.Fatalf("a1 must remain 42, got %d", a.Value)
	}
	if a := queryAgg(t, g, "v2", "a2"); a.Present {
		t.Fatalf("isolated a2 must stay absent")
	}
}

// 查询不修改聚合状态：重定源计数器与聚合结果在纯查询前后保持不变。
func TestQueryIsPure(t *testing.T) {
	g := chainGraph(t)
	mustOK(t, g.RegisterView(twoHopSpec()))
	_, err := g.AddLink("ab", "a1", "b1", LinkOptions{})
	mustOK(t, err)
	_, err = g.AddLink("bc", "b1", "c1", LinkOptions{})
	mustOK(t, err)

	snap := g.byName["v2"].snap
	beforeReach := len(snap.reach)
	for i := 0; i < 100; i++ {
		_, err := g.Query("v2", "a1")
		mustOK(t, err)
	}
	if len(snap.reach) != beforeReach || snap.rescanCost != 0 {
		t.Fatalf("query mutated aggregate state")
	}
}
