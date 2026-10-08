package ontology

import (
	"fmt"
	"testing"
)

// TestQueryCostIndependentOfGraphSize proves, via the internal access
// metric, that a query only touches the neighborhood it explores: adding
// an arbitrarily large disconnected component must not change the
// number of visited objects and examined links, nor the result.
func TestQueryCostIndependentOfGraphSize(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "m", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "m"},
		[4]string{"l2", "LB", "m", "e"},
	)
	q := Query{Start: "s", End: "e", Principal: "root", Constraint: []ConstraintPos{catPos("a"), catPos("b")}}
	res1, stats1, err := s.QueryWithStats(q)
	if err != nil || !res1.Found {
		t.Fatalf("baseline: %+v %+v %v", res1, stats1, err)
	}
	if stats1.ObjectsVisited == 0 || stats1.LinksExamined == 0 {
		t.Fatalf("metric must count actual accesses: %+v", stats1)
	}
	// Grow the graph with a large disconnected component.
	for i := 0; i < 2000; i++ {
		id := ObjectID(fmt.Sprintf("junk%d", i))
		if err := s.AddObject(id, "t0"); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			prev := ObjectID(fmt.Sprintf("junk%d", i-1))
			if err := s.AddLink(LinkID(fmt.Sprintf("jl%d", i)), "LA", prev, id); err != nil {
				t.Fatal(err)
			}
			if err := s.AddLink(LinkID(fmt.Sprintf("jm%d", i)), "LB", prev, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	res2, stats2, err := s.QueryWithStats(q)
	if err != nil || !res2.Found {
		t.Fatalf("after growth: %+v %+v %v", res2, stats2, err)
	}
	if res2.Path.TotalCost != res1.Path.TotalCost {
		t.Fatalf("result changed: %+v vs %+v", res1, res2)
	}
	if stats2 != stats1 {
		t.Fatalf("metric grew with unrelated graph size: %+v -> %+v", stats1, stats2)
	}
}
