package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

func ExampleTraverser() {
	store := ontology.NewStore(ontology.AllowAllPolicy{})
	store.AddLinkType(ontology.LinkType{ID: "edge", Direction: ontology.DirectionOut, Cost: 1})
	for _, id := range []string{"alice", "bob", "carol"} {
		store.AddObject(ontology.Object{ID: ontology.ObjectID(id)})
	}
	store.AddLink(ontology.Link{Type: "edge", From: "alice", To: "bob"})
	store.AddLink(ontology.Link{Type: "edge", From: "bob", To: "carol"})

	traverser := ontology.NewTraverser(store)
	page, err := traverser.Traverse(nil, ontology.TraverseParams{
		Start: "alice", MaxDepth: 1, MaxFanout: 10, PageSize: 10,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(page.Objects, page.Truncation)
	// Output: [alice bob] depth
}
