package ontology

import "testing"

// TestOverrideModes contrasts Replace (upstream replaced; the node's own
// grants still flow downstream) with Block (replaced and fully severed).
func TestOverrideModes(t *testing.T) {
	ctx := bg()

	g, _ := newTestGateway(t)
	mustOK(t, g.AddObjectTypes(ctx, "O", "R", "D"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "or", From: "O", To: "R", PropagationDepth: 3}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "rd", From: "R", To: "D", PropagationDepth: 3}))
	mustOK(t, g.SetOverride(ctx, "R", ReplaceOverride))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))

	d, _ := g.Decide(ctx, "s", "R", "read")
	if d.Allowed || d.DenyReason != DenialReplaced {
		t.Fatalf("replace must discard upstream union locally, got %v/%s", d.Allowed, d.DenyReason)
	}
	d, _ = g.Decide(ctx, "s", "D", "read")
	if d.Allowed || d.DenyReason != DenialReplaced {
		t.Fatalf("replaced upstream origin must not reach D, got %v/%s", d.Allowed, d.DenyReason)
	}

	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "R", Action: "read", Effect: Allow}))
	d, _ = g.Decide(ctx, "s", "D", "read")
	if !d.Allowed {
		t.Fatalf("R's own direct grant should continue downstream after Replace, reason=%s", d.DenyReason)
	}

	g2, _ := newTestGateway(t)
	mustOK(t, g2.AddObjectTypes(ctx, "O", "R", "D"))
	mustOK(t, g2.UpsertLink(ctx, LinkType{Name: "or", From: "O", To: "R", PropagationDepth: 3}))
	mustOK(t, g2.UpsertLink(ctx, LinkType{Name: "rd", From: "R", To: "D", PropagationDepth: 3}))
	mustOK(t, g2.SetOverride(ctx, "R", BlockOverride))
	mustOK(t, g2.ApplyGrant(ctx, Grant{Subject: "s", Object: "R", Action: "read", Effect: Allow}))
	d, _ = g2.Decide(ctx, "s", "D", "read")
	if d.Allowed || d.DenyReason != DenialBlocked {
		t.Fatalf("block must sever R's own continuation, got %v/%s", d.Allowed, d.DenyReason)
	}
	d, _ = g2.Decide(ctx, "s", "R", "read")
	if !d.Allowed {
		t.Fatalf("R's own explicit grant must still hold at R itself")
	}
}

// TestReplaceNotMergeWithUnion checks that replacement substitutes rather than
// merges: an upstream allow plus a downstream node with no own grant yields no
// permission, then adding the node's own grant enables only that action.
func TestReplaceNotMergeWithUnion(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "R"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "or", From: "O", To: "R", PropagationDepth: 3}))
	mustOK(t, g.SetOverride(ctx, "R", ReplaceOverride))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))

	d, _ := g.Decide(ctx, "s", "R", "read")
	if d.Allowed || d.DenyReason != DenialReplaced {
		t.Fatalf("union must be replaced, not merged: %v/%s", d.Allowed, d.DenyReason)
	}
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "R", Action: "write", Effect: Allow}))
	d, _ = g.Decide(ctx, "s", "R", "write")
	if !d.Allowed {
		t.Fatalf("R's own grant on write should now apply")
	}
	d, _ = g.Decide(ctx, "s", "R", "read")
	if d.Allowed {
		t.Fatalf("read must remain governed by replacement semantics")
	}
}
