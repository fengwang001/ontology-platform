package main

import (
	"errors"
	"fmt"

	"ontology/ontology"
)

// 演示：构建一个小型对象类型图，展示传播、覆盖与判定依据。
func main() {
	g := ontology.NewGateway(ontology.Config{})

	for _, t := range []ontology.ObjectTypeID{"Employee", "Team", "Project", "Document"} {
		g.AddObjectType(t)
	}
	must(g.AddLinkType(ontology.LinkType{
		ID: "member-of", From: "Employee", To: "Team",
		Propagates: true, MaxDepth: 2,
	}))
	must(g.AddLinkType(ontology.LinkType{
		ID: "owns", From: "Team", To: "Project",
		Propagates: true, MaxDepth: 2,
	}))
	must(g.AddLinkType(ontology.LinkType{
		ID: "contains", From: "Project", To: "Document",
		Propagates: true, MaxDepth: 1,
	}))
	must(g.AddLinkType(ontology.LinkType{
		ID: "references", From: "Document", To: "Document",
		Propagates: false, MaxDepth: 0, // 仅直接授权，不参与传播
	}))

	must(g.Grant("alice", "Employee", "read"))
	must(g.Grant("alice", "Employee", "comment"))
	must(g.SetOverride("Project", ontology.OverrideReplace))
	must(g.Grant("alice", "Project", "read"))

	for _, target := range []ontology.ObjectTypeID{"Team", "Project", "Document"} {
		dec, err := g.Decide("alice", target)
		fmt.Printf("alice -> %s: allowed=%v reason=%s err=%v\n",
			target, dec.Allowed, dec.Reason, err)
		for _, c := range dec.Contributions {
			fmt.Printf("    via %s (remaining depth %d): %v\n", c.Origin, c.Remaining, c.Actions)
		}
		for _, n := range dec.Notes {
			fmt.Printf("    note: %s\n", n)
		}
	}

	// 成环的链接类型会被拒绝且不影响既有判定。
	err := g.AddLinkType(ontology.LinkType{
		ID: "cycle", From: "Document", To: "Project",
		Propagates: true, MaxDepth: 1,
	})
	fmt.Println("add cyclic link:", err)
	if !errors.Is(err, ontology.ErrPropagationCycle) {
		panic("expected propagation cycle error")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
