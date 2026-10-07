package ontology

import "testing"

// TestDepthBoundary exercises the hop where remaining depth becomes exactly
// zero (still delivers) and the next hop (natural termination, not an error).
func TestDepthBoundary(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "A", "B", "C"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "oa", From: "O", To: "A", PropagationDepth: 2}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ab", From: "A", To: "B", PropagationDepth: 2}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "bc", From: "B", To: "C", PropagationDepth: 2}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))

	d, _ := g.Decide(ctx, "s", "B", "read")
	if !d.Allowed {
		t.Fatalf("last delivering hop should allow, reason=%s", d.DenyReason)
	}
	d, _ = g.Decide(ctx, "s", "C", "read")
	if d.Allowed || d.DenyReason != DenialDepth {
		t.Fatalf("past-budget hop must be natural depth termination, got allowed=%v reason=%s", d.Allowed, d.DenyReason)
	}
}

// TestPerLinkDepthMin checks the min(remaining, linkDepth) rule: a tight early
// link caps every later hop regardless of later links' generous depths.
func TestPerLinkDepthMin(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "A", "B"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "oa", From: "O", To: "A", PropagationDepth: 1}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ab", From: "A", To: "B", PropagationDepth: 5}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))

	d, _ := g.Decide(ctx, "s", "A", "read")
	if !d.Allowed {
		t.Fatalf("O->A must deliver")
	}
	d, _ = g.Decide(ctx, "s", "B", "read")
	if d.Allowed || d.DenyReason != DenialDepth {
		t.Fatalf("tight first link must stop further propagation at A, got %v/%s", d.Allowed, d.DenyReason)
	}
}
