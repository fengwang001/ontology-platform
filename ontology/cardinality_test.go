package ontology

import (
	"testing"
)

// 基数约束收紧后，遍历仍按 AsOf 时刻实际存在的链接集合展开，
// 不按最新约束过滤；多次调整时锚定恰好覆盖 AsOf 的那一版。
func TestCardinalityTighteningDoesNotFilterHistory(t *testing.T) {
	must := mkMust(t)
	s := NewStore()
	must(s.DefineObjectType("T", nil))
	vDefL := must(s.DefineLinkType("L", "T", "T", ManyToMany))
	for _, id := range []string{"a", "b", "c"} {
		must(s.PutObject("T", id, nil))
	}
	vAB := must(s.AddLink("L", "a", "b"))
	vAC := must(s.AddLink("L", "a", "c"))

	// 多次基数调整：ManyToMany -> OneToMany -> OneToOne -> ManyToOne。
	vAdj1 := must(s.AdjustCardinality("L", OneToMany))
	must(s.PutObject("T", "pad", nil)) // 在两次调整之间制造版本间隔
	vAdj2 := must(s.AdjustCardinality("L", OneToOne))
	vAdj3 := must(s.AdjustCardinality("L", ManyToOne))

	req := func(asOf Version) TraverseRequest {
		return TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 4, MaxVisited: 16}
	}

	// 收紧之前的时刻：两条链接都在，即使按当前最新约束它们本不被允许。
	res, err := s.Traverse(req(vAC))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 2 {
		t.Fatalf("got %d edges, want 2 (no filtering by latest cardinality)", len(res.Edges))
	}

	// 锚定版本必须恰好覆盖 AsOf，而非就近取整。
	anchorFrom := func(res *TraverseResult) Version {
		t.Helper()
		for _, d := range res.Decisions {
			if d.Kind == "cardinality-anchor" {
				return d.BasisFrom
			}
		}
		t.Fatal("no cardinality-anchor decision")
		return 0
	}
	cases := []struct {
		asOf Version
		want Version
	}{
		{vAB, vDefL},
		{vAC, vDefL},
		{vAdj1, vAdj1},
		{vAdj1 + 1, vAdj1}, // 版本区间内任一点都锚定同一版
		{vAdj2, vAdj2},
		{vAdj3, vAdj3},
	}
	for _, c := range cases {
		res, err := s.Traverse(req(c.asOf))
		if err != nil {
			t.Fatalf("asOf=%d: %v", c.asOf, err)
		}
		if got := anchorFrom(res); got != c.want {
			t.Fatalf("asOf=%d: anchored at version From=%d, want %d", c.asOf, got, c.want)
		}
	}
}

// 写入时的基数校验使用恰好覆盖写入时刻的约束版本。
func TestCardinalityEnforcedAtWriteTime(t *testing.T) {
	must := mkMust(t)
	s := NewStore()
	must(s.DefineObjectType("T", nil))
	must(s.DefineLinkType("L", "T", "T", ManyToMany))
	for _, id := range []string{"a", "b", "c"} {
		must(s.PutObject("T", id, nil))
	}
	must(s.AddLink("L", "a", "b"))

	// 收紧为 OneToOne 后，a 不能再有出链。
	must(s.AdjustCardinality("L", OneToOne))
	if _, err := s.AddLink("L", "a", "c"); err != ErrCardinalityViolate {
		t.Fatalf("got %v, want ErrCardinalityViolate", err)
	}
	// 放宽回 ManyToMany 后允许。
	must(s.AdjustCardinality("L", ManyToMany))
	must(s.AddLink("L", "a", "c"))
}
