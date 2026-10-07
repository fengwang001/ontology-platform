package ontology

import "testing"

// TestPropagationCycle ensures cycles among propagation-enabled links are
// rejected as a whole, while depth-0 links never participate in a cycle, and a
// rejected batch leaves graph and answers untouched.
func TestPropagationCycle(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "A", "B", "C"))

	// A depth-0 back-edge exists structurally but must not create a cycle.
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ab", From: "A", To: "B", PropagationDepth: 2}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ba0", From: "B", To: "A", PropagationDepth: 0}))

	err := g.Apply(ctx,
		Change{Kind: UpsertLinkChange, Link: LinkType{Name: "bc", From: "B", To: "C", PropagationDepth: 1}},
		Change{Kind: UpsertLinkChange, Link: LinkType{Name: "ca", From: "C", To: "A", PropagationDepth: 1}},
	)
	if errKind(err) != KindPropagationCycle {
		t.Fatalf("want propagation cycle, got %v", err)
	}

	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "A", Action: "read", Effect: Allow}))
	d, err := g.Decide(ctx, "s", "B", "read")
	mustOK(t, err)
	if !d.Allowed {
		t.Fatalf("A->B should still deliver after rejected batch, reason=%s", d.DenyReason)
	}
	if _, exists := g.snapshot().links["bc"]; exists {
		t.Fatalf("rejected link bc must not exist in committed state")
	}
}
