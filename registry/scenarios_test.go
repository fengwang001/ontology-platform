package registry

import (
	"reflect"
	"testing"
)

func TestDeadManifestIsLiveWhenReachedByLiveIndex(t *testing.T) {
	r, err := NewReclaimer(0)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer", 1, 1), "PutLayer")
	mustDo(t, r.PutManifest("manifest", []string{"layer"}, 2), "PutManifest")
	mustDo(t, r.PutIndex("index", []string{"manifest"}, 3), "PutIndex")
	mustDo(t, r.Tag("multi", "index", 4), "Tag index")

	t.Logf("input=tag -> index -> manifest -> layer; output=index/manifest/layer live=%t/%t/%t; basis=reachable through a live index", r.Live("index"), r.Live("manifest"), r.Live("layer"))
	if !r.Live("index") || !r.Live("manifest") || !r.Live("layer") {
		t.Fatal("all objects reached by the tagged index must be live")
	}

	deleted, err := r.GC(5)
	mustDo(t, err, "GC")
	t.Logf("input=GC(5) while tagged index is reachable; output=%v; basis=live objects cannot be deleted", deleted)
	if len(deleted) != 0 {
		t.Fatalf("GC deleted reachable objects %v", deleted)
	}
}

func TestRevivalClearsDeadSinceAndRestartsLater(t *testing.T) {
	r, err := NewReclaimer(100)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer", 1, 1), "PutLayer")
	mustDo(t, r.PutManifest("manifest", []string{"layer"}, 2), "PutManifest")
	mustDo(t, r.Tag("v1", "manifest", 10), "Tag")
	mustDo(t, r.Untag("v1", 20), "Untag")
	if got := deadSinceOf(t, r, "manifest"); got != 20 {
		t.Fatalf("manifest deadSince after untag = %d, want 20", got)
	}

	mustDo(t, r.Tag("v1", "manifest", 30), "Retag")
	t.Logf("input=retag dead manifest at 30; output=live=%t; basis=deadSince is cleared when dead object becomes live", r.Live("manifest"))
	if !r.Live("manifest") || r.objects["manifest"].deadSinceSet {
		t.Fatal("revived manifest must be live and have no deadSince")
	}

	mustDo(t, r.Untag("v1", 40), "Untag again")
	got := deadSinceOf(t, r, "manifest")
	t.Logf("input=second loss of reachability at 40; output=deadSince %d; basis=retention restarts from the latest transition", got)
	if got != 40 {
		t.Fatalf("manifest deadSince after second loss = %d, want 40", got)
	}
}

func TestUnexpiredDeadManifestKeepsExpiredLayer(t *testing.T) {
	r, err := NewReclaimer(100)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer", 1, 1), "PutLayer")
	mustDo(t, r.PutManifest("expired-manifest", []string{"layer"}, 10), "PutManifest expired referrer")
	mustDo(t, r.Tag("v1", "expired-manifest", 20), "Tag first referrer")
	mustDo(t, r.Untag("v1", 50), "Untag first referrer")
	mustDo(t, r.PutManifest("retained-manifest", []string{"layer"}, 150), "PutManifest unexpired referrer")

	deleted, err := r.GC(200)
	mustDo(t, err, "GC")
	t.Logf("input=layer expired at 200, retained-manifest deadSince=150 and is unexpired, R=100; output=%v; basis=an existing dead manifest still refers to the layer", deleted)
	if !reflect.DeepEqual(deleted, []string{"expired-manifest"}) {
		t.Fatalf("GC = %v, want only expired-manifest", deleted)
	}

	deleted, err = r.GC(250)
	mustDo(t, err, "second GC")
	t.Logf("input=GC(250) after retained-manifest expires; output=%v; basis=manifest is deleted first and then makes layer unreferenced", deleted)
	if !reflect.DeepEqual(deleted, []string{"retained-manifest", "layer"}) {
		t.Fatalf("GC = %v, want [retained-manifest layer]", deleted)
	}
}

func TestGCCascadesByKindAndDigestOrder(t *testing.T) {
	r, err := NewReclaimer(0)
	mustDo(t, err, "NewReclaimer")

	mustDo(t, r.PutLayer("layer-b", 1, 1), "PutLayer b")
	mustDo(t, r.PutLayer("layer-a", 1, 2), "PutLayer a")
	mustDo(t, r.PutManifest("manifest-b", []string{"layer-b"}, 3), "PutManifest b")
	mustDo(t, r.PutManifest("manifest-a", []string{"layer-a"}, 4), "PutManifest a")
	mustDo(t, r.PutIndex("index-b", []string{"manifest-b"}, 5), "PutIndex b")
	mustDo(t, r.PutIndex("index-a", []string{"manifest-a"}, 6), "PutIndex a")

	deleted, err := r.GC(7)
	mustDo(t, err, "GC")
	want := []string{"index-a", "index-b", "manifest-a", "manifest-b", "layer-a", "layer-b"}
	t.Logf("input=all objects are dead and expired; output=%v; basis=indices then manifests then layers, each sorted by digest; deletion cascades immediately", deleted)
	if !reflect.DeepEqual(deleted, want) {
		t.Fatalf("GC = %v, want %v", deleted, want)
	}
}

func TestDuplicateLayerUploadDoesNotResetDeadSince(t *testing.T) {
	r, err := NewReclaimer(10)
	mustDo(t, err, "NewReclaimer")
	mustDo(t, r.PutLayer("layer", 7, 1), "initial PutLayer")
	mustDo(t, r.PutLayer("layer", 7, 9), "duplicate PutLayer")

	got := deadSinceOf(t, r, "layer")
	t.Logf("input=PutLayer(layer,7,1) then identical PutLayer(layer,7,9); output=deadSince %d; basis=idempotent upload changes no object metadata", got)
	if got != 1 {
		t.Fatalf("deadSince = %d, want 1", got)
	}

	deleted, err := r.GC(11)
	mustDo(t, err, "GC")
	t.Logf("input=GC(11) with original deadSince 1, R=10; output=%v; basis=the duplicate upload did not reset retention", deleted)
	if !reflect.DeepEqual(deleted, []string{"layer"}) {
		t.Fatalf("GC = %v, want [layer]", deleted)
	}
}
