package registry

import "testing"

func mustDo(t *testing.T, err error, action string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed: %v", action, err)
	}
}

func deadSinceOf(t *testing.T, r *Reclaimer, digest string) int64 {
	t.Helper()
	obj, ok := r.objects[digest]
	if !ok {
		t.Fatalf("object %s does not exist", digest)
	}
	if !obj.deadSinceSet {
		t.Fatalf("object %s is live, so deadSince must be cleared", digest)
	}
	return obj.deadSince
}

func TestSharedLayerDeadSinceUsesLastLiveReferrer(t *testing.T) {
	r, err := NewReclaimer(100)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer", 10, 1), "PutLayer")
	mustDo(t, r.PutManifest("dead-manifest", []string{"layer"}, 2), "PutManifest(dead)")
	mustDo(t, r.PutManifest("live-manifest", []string{"layer"}, 3), "PutManifest(live)")
	mustDo(t, r.Tag("v1", "live-manifest", 10), "Tag live manifest")
	mustDo(t, r.Untag("v1", 20), "Untag live manifest")

	got := deadSinceOf(t, r, "layer")
	t.Logf("input=shared layer referenced by live-manifest until 20 and dead-manifest since 3; output=deadSince %d; basis=deadSince is the last transition from live to dead", got)
	if got != 20 {
		t.Fatalf("layer deadSince = %d, want 20", got)
	}
}

func TestDeadSinceStartsWhenTagMovedAway(t *testing.T) {
	r, err := NewReclaimer(10)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer", 10, 0), "PutLayer")
	mustDo(t, r.PutManifest("manifest", []string{"layer"}, 1), "PutManifest")
	mustDo(t, r.PutManifest("other", []string{"layer"}, 2), "PutManifest(other)")
	mustDo(t, r.Tag("v1", "manifest", 10), "Tag original")
	mustDo(t, r.Tag("v1", "other", 20), "Tag moved")

	got := deadSinceOf(t, r, "manifest")
	t.Logf("input=manifest created at 1, tagged at 10, tag moved away at 20; output=deadSince %d; basis=retention starts at loss of reachability", got)
	if got != 20 {
		t.Fatalf("manifest deadSince = %d, want 20", got)
	}

	kept, err := r.GC(29)
	mustDo(t, err, "GC one millisecond early")
	t.Logf("input=GC(29), R=10; output=%v; basis=29-20 < 10", kept)
	if len(kept) != 0 {
		t.Fatalf("GC before exact boundary deleted %v", kept)
	}
}

func TestGCBoundaryIsInclusive(t *testing.T) {
	r, err := NewReclaimer(10)
	mustDo(t, err, "NewReclaimer")
	mustDo(t, r.PutLayer("layer", 1, 0), "PutLayer")

	kept, err := r.GC(9)
	mustDo(t, err, "GC(9)")
	t.Logf("input=GC(9) for dead object since 0, R=10; output=%v; basis=9-0 < 10", kept)
	if len(kept) != 0 {
		t.Fatalf("GC(9) deleted %v", kept)
	}

	deleted, err := r.GC(10)
	mustDo(t, err, "GC(10)")
	t.Logf("input=GC(10) for dead object since 0, R=10; output=%v; basis=10-0 == 10", deleted)
	if len(deleted) != 1 || deleted[0] != "layer" {
		t.Fatalf("GC(10) = %v, want [layer]", deleted)
	}
}
