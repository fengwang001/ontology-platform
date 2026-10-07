package ontology

import (
	"bytes"
	"sync"
	"testing"
)

// TestUnionAndDenyWins verifies path union semantics and deny propagation.
func TestUnionAndDenyWins(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "A", "B", "T"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "at1", From: "A", To: "T", PropagationDepth: 1}))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "bt", From: "B", To: "T", PropagationDepth: 1}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "A", Action: "read", Effect: Allow}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "B", Action: "read", Effect: Deny}))

	d, _ := g.Decide(ctx, "s", "T", "read")
	if d.Allowed || d.DenyReason != DenialExplicit {
		t.Fatalf("deny along one path must win the union, got %v/%s", d.Allowed, d.DenyReason)
	}

	mustOK(t, g.RevokeGrant(ctx, Grant{Subject: "s", Object: "B", Action: "read"}))
	d, _ = g.Decide(ctx, "s", "T", "read")
	if !d.Allowed {
		t.Fatalf("union of remaining allow paths should permit")
	}
}

// TestExplicitBeatsPropagation verifies explicit records at the target
// dominate propagated content regardless of coverage breadth.
func TestExplicitBeatsPropagation(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "T"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ot", From: "O", To: "T", PropagationDepth: 2}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "T", Action: "read", Effect: Deny}))

	d, _ := g.Decide(ctx, "s", "T", "read")
	if d.Allowed || d.DenyReason != DenialExplicit {
		t.Fatalf("explicit deny at target must beat propagated allow")
	}

	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "T", Action: "read", Effect: Allow}))
	d, _ = g.Decide(ctx, "s", "T", "read")
	if !d.Allowed {
		t.Fatalf("explicit allow replacement must be observable at once")
	}
}

// TestDecisionLogger verifies input/output/basis logging per decision.
func TestDecisionLogger(t *testing.T) {
	g, log := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "T"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ot", From: "O", To: "T", PropagationDepth: 1}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "alice", Object: "O", Action: "read", Effect: Allow}))
	if _, err := g.Decide(ctx, "alice", "T", "read"); err != nil {
		t.Fatal(err)
	}
	line := log.String()
	for _, want := range []string{"subject=alice", "object=T", "action=read", "=> allow", "basis=["} {
		if !bytes.Contains([]byte(line), []byte(want)) {
			t.Fatalf("log missing %q: %s", want, line)
		}
	}
}

// TestConcurrentRejectedBatchInert runs a cycle-creating rejected batch while
// readers continuously query; every observed answer must match a committed
// serial prefix.
func TestConcurrentRejectedBatchInert(t *testing.T) {
	g, _ := newTestGateway(t)
	ctx := bg()
	mustOK(t, g.AddObjectTypes(ctx, "O", "T", "X"))
	mustOK(t, g.UpsertLink(ctx, LinkType{Name: "ot", From: "O", To: "T", PropagationDepth: 2}))
	mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "s", Object: "O", Action: "read", Effect: Allow}))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				d, err := g.Decide(ctx, "s", "T", "read")
				if err != nil || !d.Allowed {
					t.Errorf("reader observed non-serializable answer: %+v %v", d, err)
					return
				}
			}
		}
	}()

	for i := 0; i < 50; i++ {
		err := g.Apply(ctx,
			Change{Kind: UpsertLinkChange, Link: LinkType{Name: "tx", From: "T", To: "X", PropagationDepth: 1}},
			Change{Kind: UpsertLinkChange, Link: LinkType{Name: "xo", From: "X", To: "O", PropagationDepth: 1}},
		)
		if errKind(err) != KindPropagationCycle {
			t.Fatalf("cycle batch must be rejected, got %v", err)
		}
	}
	close(stop)
	wg.Wait()

	if _, exists := g.snapshot().links["tx"]; exists {
		t.Fatalf("rejected link must not persist")
	}
}
