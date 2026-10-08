package ontology

import (
	"fmt"
	"testing"
)

// buildMetricsGraph 构造一个起点邻域很小的图：s -cA-> a -cA-> t，
// 另有 s -cA-> t 直达（代价 10）。
func buildMetricsGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := NewGraph([]ObjectTypeSpec{{ID: "T1"}}, []Category{"cA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterLinkType(LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1}); err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterLinkType(LinkTypeSpec{ID: "LA10", From: "T1", To: "T1", Category: "cA", Cost: 10}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ObjectID{"s", "a", "t"} {
		if err := g.AddObject(id, "T1"); err != nil {
			t.Fatal(err)
		}
	}
	links := []struct {
		id       LinkID
		typ      LinkTypeID
		from, to ObjectID
	}{
		{"l1", "LA", "s", "a"},
		{"l2", "LA", "a", "t"},
		{"l3", "LA10", "s", "t"},
	}
	for _, l := range links {
		if err := g.AddLink(l.id, l.typ, l.from, l.to); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

// TestQueryMetricsIgnoreUnrelatedBulk 验证内部度量只统计本次查询实际访问
// 的对象与链接：向图中加入大量与起点终点无关的对象和链接后，同一查询的
// 度量必须保持不变。
func TestQueryMetricsIgnoreUnrelatedBulk(t *testing.T) {
	g := buildMetricsGraph(t)
	q := PathQuery{Start: "s", End: "t", Pattern: []PatternElem{{Category: "cA", Star: true}}}

	res1, m1, err := g.query(q)
	if err != nil || !res1.Found {
		t.Fatalf("baseline query: res=%+v err=%v", res1, err)
	}
	if m1.ObjectsVisited == 0 || m1.LinksVisited == 0 {
		t.Fatalf("metrics should count visited objects/links, got %+v", m1)
	}

	// 加入一个与查询无关的大规模连通区域（含大量对象与链接）。
	const bulkObjects = 3000
	for i := 0; i < bulkObjects; i++ {
		if err := g.AddObject(ObjectID(fmt.Sprintf("bulk-%d", i)), "T1"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < bulkObjects-1; i++ {
		id := LinkID(fmt.Sprintf("bulk-link-%d", i))
		from := ObjectID(fmt.Sprintf("bulk-%d", i))
		to := ObjectID(fmt.Sprintf("bulk-%d", i+1))
		if err := g.AddLink(id, "LA", from, to); err != nil {
			t.Fatal(err)
		}
	}
	// 再加一个挂在终点远端、代价很高的区域，确保它也不会被访问。
	for i := 0; i < 500; i++ {
		id := ObjectID(fmt.Sprintf("far-%d", i))
		if err := g.AddObject(id, "T1"); err != nil {
			t.Fatal(err)
		}
		prev := ObjectID("t")
		if i > 0 {
			prev = ObjectID(fmt.Sprintf("far-%d", i-1))
		}
		if err := g.AddLink(LinkID(fmt.Sprintf("far-link-%d", i)), "LA10", prev, id); err != nil {
			t.Fatal(err)
		}
	}

	res2, m2, err := g.query(q)
	if err != nil || !res2.Found {
		t.Fatalf("query after bulk insert: res=%+v err=%v", res2, err)
	}
	if res2.Cost != res1.Cost {
		t.Fatalf("cost changed after bulk insert: %d -> %d", res1.Cost, res2.Cost)
	}
	if m1 != m2 {
		t.Fatalf("metrics grew with unrelated graph size: before=%+v after=%+v", m1, m2)
	}
	t.Logf("metrics stable at %+v while graph grew to %d objects", m2, bulkObjects+500+3)
}
