package ontology

import "testing"

// TestObjectNotFound verifies the highest-priority error class.
func TestObjectNotFound(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "A"))
	if _, err := g.Decide(ctx, "s", "ghost", "read"); errKind(err) != KindObjectNotFound {
		t.Fatalf("want object-not-found, got %v", err)
	}
	if err := g.ApplyGrant(ctx, Grant{"s", "ghost", "read", Allow}); errKind(err) != KindObjectNotFound {
		t.Fatalf("grant on missing type should fail, got %v", err)
	}
	if err := g.UpsertLink(ctx, LinkType{Name: "l", From: "A", To: "ghost", PropagationDepth: 1}); errKind(err) != KindObjectNotFound {
		t.Fatalf("link to missing type should fail, got %v", err)
	}
}

// TestInvalidDepth covers negative depth and depth above the platform cap.
func TestInvalidDepth(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "A", "B"))
	if err := g.UpsertLink(ctx, LinkType{Name: "l", From: "A", To: "B", PropagationDepth: -1}); errKind(err) != KindInvalidDepth {
		t.Fatalf("want invalid depth, got %v", err)
	}
	if err := g.UpsertLink(ctx, LinkType{Name: "l", From: "A", To: "B", PropagationDepth: MaxPropagationDepth + 1}); errKind(err) != KindInvalidDepth {
		t.Fatalf("want cap-exceeded depth, got %v", err)
	}
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "l", From: "A", To: "B", PropagationDepth: MaxPropagationDepth}))
}

// TestConflictingOverride verifies two irreconcilable overrides in one batch
// are rejected with the dedicated error kind.
func TestConflictingOverride(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "T"))
	err := g.Apply(ctx,
		Change{Kind: SetOverrideChange, Object: "T", Override: ReplaceOverride},
		Change{Kind: SetOverrideChange, Object: "T", Override: BlockOverride},
	)
	if errKind(err) != KindConflictingOverride {
		t.Fatalf("want conflicting override, got %v", err)
	}
}
