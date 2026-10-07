package ontology_test

import (
	"fmt"

	"ontology"
)

func ExampleGraph_hasCycle() {
	g := ontology.NewGraph()
	_ = g.AddObjectType("T", "Thing")
	_ = g.AddLinkType(ontology.LinkType{
		ID:            "to",
		Direction:     ontology.Directed,
		AllowSelfLoop: true,
		AllowParallel: true,
	})
	_ = g.AddObject("alice", "a", "T")
	_ = g.AddObject("alice", "b", "T")
	_ = g.AddLink("alice", "ab", "to", "a", "b")
	_ = g.AddLink("alice", "ba", "to", "b", "a")

	res, _ := g.HasCycle("alice")
	fmt.Println(res.HasCycle, res.Evidence)
	// Output: true [a b a]
}

func ExampleGraph_permissionFilter() {
	g := ontology.NewGraph()
	_ = g.AddObjectType("T", "Thing")
	_ = g.AddLinkType(ontology.LinkType{
		ID: "to", Direction: ontology.Directed,
		AllowSelfLoop: true, AllowParallel: true,
	})
	_ = g.AddObject("admin", "a", "T")
	_ = g.AddObject("admin", "b", "T")
	_ = g.AddLink("admin", "ab", "to", "a", "b")
	_ = g.AddLink("admin", "ba", "to", "b", "a")

	// bob can traverse both links but cannot see object b: existence filter
	// runs first and removes both links.
	_ = g.GrantExistence("bob", "a")
	_ = g.GrantTraversal("bob", "ab", "ba")
	res, _ := g.HasCycle("bob")
	fmt.Println(res.HasCycle)

	_ = g.GrantExistence("bob", "b")
	res, _ = g.HasCycle("bob")
	fmt.Println(res.HasCycle, res.Evidence)
	// Output:
	// false
	// true [a b a]
}
