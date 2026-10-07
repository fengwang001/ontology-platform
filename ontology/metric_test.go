package ontology

import (
	"fmt"
	"testing"
)

// TestMetricIndependentOfRegistryAndGraphSize 验证内部度量只随本次查询
// 实际评估的链接/对象类型数目增长，与权限组总数及图总体规模无关。
func TestMetricIndependentOfRegistryAndGraphSize(t *testing.T) {
	build := func(groups, noiseObjects int) *Service {
		reg := NewPermissionRegistry(DenyOverrides)
		// 大量与本次查询无关的权限组与声明。
		for i := 0; i < groups; i++ {
			g := GroupID(fmt.Sprintf("g%d", i))
			reg.UpsertGroup(g, i%7)
			reg.SetLinkTypeDeclaration(g, LinkTypeID(fmt.Sprintf("NL%d", i)), DeclDeny)
			reg.SetObjectTypeDeclaration(g, ObjectTypeID(fmt.Sprintf("NT%d", i)), DeclDeny)
		}
		reg.UpsertGroup("user", 1)
		reg.AssignPrincipal("p", "user")
		reg.SetLinkTypeDeclaration("user", "L1", DeclAllow)
		reg.SetLinkTypeDeclaration("user", "L2", DeclAllow)

		g := NewGraph()
		// 本次查询涉及的小邻域：S -L1-> X -L2-> T。
		g.AddObject("S", "TS")
		g.AddObject("X", "TX")
		g.AddObject("T", "TT")
		mustLink(t, g, "S", "X", "L1", 1)
		mustLink(t, g, "X", "T", "L2", 1)
		// 大量与查询无关的对象与链接。
		for i := 0; i < noiseObjects; i++ {
			id := ObjectID(fmt.Sprintf("N%d", i))
			g.AddObject(id, ObjectTypeID(fmt.Sprintf("NT%d", i)))
			if i > 0 {
				prev := ObjectID(fmt.Sprintf("N%d", i-1))
				mustLink(t, g, prev, id, "NL", 1)
			}
		}
		return NewService(reg, g)
	}

	small := build(50, 100)
	res1, err := small.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res1.Status != StatusReachable {
		t.Fatalf("status = %v", res1.Status)
	}
	m1 := res1.metric

	large := build(5000, 20000)
	res2, err := large.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	m2 := res2.metric

	if m1 != m2 {
		t.Fatalf("metric grew with registry/graph size: %+v -> %+v", m1, m2)
	}
	// 链接类型层全部命中声明，对象类型层一次都不应评估；
	// 链接类型评估数等于实际探索到的不同链接类型数（L1、L2）。
	if m1.objectTypeEvals != 0 {
		t.Fatalf("objectTypeEvals = %d, want 0 (link layer declared)", m1.objectTypeEvals)
	}
	if m1.linkTypeEvals != 2 {
		t.Fatalf("linkTypeEvals = %d, want 2", m1.linkTypeEvals)
	}
}

// TestMetricCountsObjectLayerFallback 验证链接类型层未声明时才会
// 回退评估对象类型层，且度量按查询内去重后的实际计算计数。
func TestMetricCountsObjectLayerFallback(t *testing.T) {
	reg := NewPermissionRegistry(DenyOverrides)
	reg.UpsertGroup("g", 1)
	reg.AssignPrincipal("p", "g")
	reg.SetObjectTypeDeclaration("g", "TS", DeclAllow)
	reg.SetObjectTypeDeclaration("g", "TA", DeclAllow)
	reg.SetObjectTypeDeclaration("g", "TT", DeclAllow)

	g := NewGraph()
	g.AddObject("S", "TS")
	g.AddObject("A", "TA")
	g.AddObject("T", "TT")
	mustLink(t, g, "S", "A", "L", 1)
	mustLink(t, g, "A", "T", "L", 1)
	mustLink(t, g, "S", "T", "L", 5)
	svc := NewService(reg, g)

	res, err := svc.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusReachable {
		t.Fatalf("status = %v", res.Status)
	}
	// 链接类型只有 L 一种（查询内去重后计 1 次）；对象类型 TS/TA/TT 各计 1 次。
	if res.metric.linkTypeEvals != 1 {
		t.Fatalf("linkTypeEvals = %d, want 1", res.metric.linkTypeEvals)
	}
	if res.metric.objectTypeEvals != 3 {
		t.Fatalf("objectTypeEvals = %d, want 3", res.metric.objectTypeEvals)
	}
}
