// Command server runs a scripted demonstration of the permission propagation
// gateway: propagation along typed directed links, per-link depth budgets,
// replace/block overrides, and distinguishable denial outcomes.
package main

import (
	"context"
	"fmt"
	"os"

	"ontology/ontology"
)

func main() {
	ctx := context.Background()
	gw := ontology.NewGateway(ontology.WriteLogger{W: os.Stdout})

	must := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "fatal:", err)
			os.Exit(1)
		}
	}

	must(gw.AddObjectTypes(ctx, "Project", "Folder", "Doc", "Archive"))

	// Project -> Folder -> Doc participate in propagation; Doc -> Archive is a
	// depth-0 link usable only for direct authorization.
	must(gw.UpsertLink(ctx, ontology.LinkType{Name: "contains", From: "Project", To: "Folder", PropagationDepth: 2}))
	must(gw.UpsertLink(ctx, ontology.LinkType{Name: "holds", From: "Folder", To: "Doc", PropagationDepth: 2}))
	must(gw.UpsertLink(ctx, ontology.LinkType{Name: "archives", From: "Doc", To: "Archive", PropagationDepth: 0}))

	must(gw.ApplyGrant(ctx, ontology.Grant{Subject: "alice", Object: "Project", Action: "read", Effect: ontology.Allow}))

	show := func(subject, object, action string) {
		d, err := gw.Decide(ctx, subject, object, action)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("verdict %s/%s/%s -> allowed=%v reason=%s\n\n", subject, object, action, d.Allowed, d.DenyReason)
	}

	show("alice", "Folder", "read")  // propagated, remaining depth 1
	show("alice", "Doc", "read")     // propagated, last delivering hop (remaining 0)
	show("alice", "Archive", "read") // depth-0 link: natural termination

	// A block override on Doc severs further propagation and replaces its
	// upstream union with Doc's own direct authorizations.
	must(gw.SetOverride(ctx, "Doc", ontology.BlockOverride))
	show("alice", "Doc", "read")

	// A cycle-creating change is rejected atomically and changes nothing.
	err := gw.UpsertLink(ctx, ontology.LinkType{Name: "loop", From: "Doc", To: "Project", PropagationDepth: 1})
	fmt.Println("cycle change rejected with:", err)
}
