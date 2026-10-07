package ontology

import (
	"fmt"
	"testing"
)

// TestTraversalCostIndependentOfNetworkSize is the observable proof that
// the cost of one access check depends only on the tag sources and role
// nodes relevant to the instance, not on the total size of the relation
// network: growing the network by an order of magnitude with unrelated
// types, links, tags and roles leaves the traversal counters unchanged.
func TestTraversalCostIndependentOfNetworkSize(t *testing.T) {
	e := NewEngine()
	must(t, e.DeclareObjectType("Doc"))
	must(t, e.DeclareObjectType("Folder"))
	must(t, e.DeclareLinkType("contains", "Folder", "Doc"))
	must(t, e.DeclareTag("confidential"))
	must(t, e.DeclareTag("restricted"))
	must(t, e.AttachTag("confidential", "Folder"))
	must(t, e.AttachTag("restricted", "Doc"))
	must(t, e.DeclarePropagation("confidential", "contains", Downstream))
	must(t, e.DeclareRole("analyst"))
	must(t, e.DeclareRole("lead", "analyst"))
	must(t, e.DeclareSubject("alice", "analyst", "lead"))
	must(t, e.SetGrant("analyst", "confidential", Allow))
	must(t, e.SetGrant("lead", "restricted", Allow))
	must(t, e.DeclareInstance("doc-1", "Doc"))
	dec, err := e.Authorize("alice", "doc-1", "read")
	must(t, err)
	if !dec.Allowed {
		t.Fatalf("expected allow, got %+v", dec)
	}
	before := dec.Stats
	if before.TagSourcesEvaluated != 2 {
		t.Fatalf("expected 2 tag sources, got %d", before.TagSourcesEvaluated)
	}
	if before.RoleNodesVisited != 2 {
		t.Fatalf("expected 2 role nodes, got %d", before.RoleNodesVisited)
	}
	// Grow the network 100x with unrelated structure: extra types, links,
	// tags, propagations, blocks, roles and grants, none of which reach
	// doc-1 or alice.
	for i := 0; i < 100; i++ {
		ot := fmt.Sprintf("NoiseType%d", i)
		must(t, e.DeclareObjectType(ot))
		must(t, e.DeclareLinkType(fmt.Sprintf("noise-link-%d", i), ot, "Doc"))
		tag := fmt.Sprintf("noise-tag-%d", i)
		must(t, e.DeclareTag(tag))
		must(t, e.AttachTag(tag, ot))
		must(t, e.DeclareBlock(tag, fmt.Sprintf("noise-link-%d", i)))
		role := fmt.Sprintf("noise-role-%d", i)
		must(t, e.DeclareRole(role))
		must(t, e.SetGrant(role, tag, Allow))
		must(t, e.DeclareInstance(fmt.Sprintf("noise-inst-%d", i), ot))
	}
	dec2, err := e.Authorize("alice", "doc-1", "read")
	must(t, err)
	if dec2.Stats != before {
		t.Fatalf("traversal cost grew with network size: before=%+v after=%+v", before, dec2.Stats)
	}
	if !dec2.Allowed {
		t.Fatalf("decision changed after unrelated growth: %+v", dec2)
	}
	// The same invariance holds for tag-specific checks.
	ct1, err := e.CheckTag("alice", "doc-1", "confidential")
	must(t, err)
	if ct1.Stats.TagSourcesEvaluated != 1 || ct1.Stats.RoleNodesVisited != 2 {
		t.Fatalf("unexpected CheckTag stats: %+v", ct1.Stats)
	}
}
