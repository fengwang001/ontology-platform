package ontology

import (
	"sync"
	"testing"
)

func deadState(t *testing.T, r *Reclaimer, digest string, alive bool, deadSince int64, hasDeadSince bool) {
	t.Helper()

	obj, ok := r.objects[digest]
	if !ok {
		t.Fatalf("digest %q does not exist", digest)
	}
	if obj.alive != alive {
		t.Fatalf("digest %q alive = %v, want %v", digest, obj.alive, alive)
	}
	if obj.deadSince != deadSince {
		t.Fatalf("digest %q deadSince = %d, want %d", digest, obj.deadSince, deadSince)
	}
	if obj.hasDeadSince != hasDeadSince {
		t.Fatalf("digest %q hasDeadSince = %v, want %v", digest, obj.hasDeadSince, hasDeadSince)
	}
}

func TestRetentionBoundary(t *testing.T) {
	r, err := NewReclaimer(10)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.PutLayer(100, "layer-a", 1); err != nil {
		t.Fatal(err)
	}

	removed, err := r.GC(109)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("GC at 109 removed %v, want nothing", removed)
	}

	removed, err = r.GC(110)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "layer-a" {
		t.Fatalf("GC at 110 removed %v, want [layer-a]", removed)
	}
}

func TestSharedLayerUsesLastLiveReferencerDeath(t *testing.T) {
	r, err := NewReclaimer(100)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "shared-layer", 10))
	must(t, r.PutManifest(2, "dead-manifest", []string{"shared-layer"}))
	must(t, r.PutManifest(3, "live-manifest", []string{"shared-layer"}))
	must(t, r.Tag(4, "release", "live-manifest"))

	deadState(t, r, "shared-layer", true, 0, false)

	must(t, r.Untag(5, "release"))
	deadState(t, r, "shared-layer", false, 5, true)
}

func TestDeadManifestLivesThroughLiveIndex(t *testing.T) {
	r, err := NewReclaimer(100)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "layer-a", 1))
	must(t, r.PutManifest(2, "manifest-a", []string{"layer-a"}))
	must(t, r.PutIndex(3, "index-a", []string{"manifest-a"}))
	must(t, r.Tag(4, "multiarch", "index-a"))

	deadState(t, r, "manifest-a", true, 0, false)
	deadState(t, r, "layer-a", true, 0, false)
}

func TestRetagRevivesAndRestartsDeadSince(t *testing.T) {
	r, err := NewReclaimer(100)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "layer-a", 1))
	must(t, r.PutManifest(2, "manifest-a", []string{"layer-a"}))
	must(t, r.Tag(3, "release", "manifest-a"))
	must(t, r.Untag(10, "release"))
	deadState(t, r, "layer-a", false, 10, true)

	must(t, r.Tag(20, "release", "manifest-a"))
	deadState(t, r, "layer-a", true, 0, false)

	must(t, r.Untag(30, "release"))
	deadState(t, r, "layer-a", false, 30, true)

	if removed, err := r.GC(129); err != nil || len(removed) != 0 {
		t.Fatalf("GC at 129 = %v, %v; want no deletion", removed, err)
	}
	if removed, err := r.GC(130); err != nil || !sameStrings(removed, []string{"manifest-a", "layer-a"}) {
		t.Fatalf("GC at 130 = %v, %v; want [manifest-a layer-a]", removed, err)
	}
}

func TestUnexpiredDeadManifestKeepsExpiredLayer(t *testing.T) {
	r, err := NewReclaimer(100)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "layer-a", 1))
	must(t, r.PutManifest(2, "manifest-a", []string{"layer-a"}))
	must(t, r.Tag(3, "release", "manifest-a"))

	must(t, r.PutLayer(10, "orphan-layer", 1))
	must(t, r.Untag(20, "release"))

	removed, err := r.GC(109)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("GC at 109 = %v, want no deletion", removed)
	}

	removed, err = r.GC(110)
	if err != nil || !sameStrings(removed, []string{"orphan-layer"}) {
		t.Fatalf("GC at 110 = %v, %v; want only orphan layer", removed, err)
	}
	if !r.Exists("layer-a") || !r.Exists("manifest-a") {
		t.Fatalf("referenced layer and unexpired manifest must remain")
	}

	removed, err = r.GC(119)
	if err != nil || len(removed) != 0 {
		t.Fatalf("GC at 119 = %v, %v; want manifest retained one millisecond before expiry", removed, err)
	}

	removed, err = r.GC(120)
	if err != nil || !sameStrings(removed, []string{"manifest-a", "layer-a"}) {
		t.Fatalf("GC at 120 = %v, %v; want cascade [manifest-a layer-a]", removed, err)
	}
}

func TestGCCascadesIndexManifestAndLayers(t *testing.T) {
	r, err := NewReclaimer(0)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "layer-a", 1))
	must(t, r.PutLayer(2, "layer-b", 1))
	must(t, r.PutManifest(3, "manifest-a", []string{"layer-a", "layer-b"}))
	must(t, r.PutIndex(4, "index-a", []string{"manifest-a"}))
	must(t, r.Tag(5, "release", "index-a"))
	must(t, r.Untag(6, "release"))

	removed, err := r.GC(7)
	if err != nil || !sameStrings(removed, []string{"index-a", "manifest-a", "layer-a", "layer-b"}) {
		t.Fatalf("GC = %v, %v; want deterministic cascade", removed, err)
	}
}

func TestDuplicateLayerUploadDoesNotResetDeadSince(t *testing.T) {
	r, err := NewReclaimer(10)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(1, "layer-a", 42))
	must(t, r.PutLayer(2, "layer-a", 42))
	deadState(t, r, "layer-a", false, 1, true)

	removed, err := r.GC(10)
	if err != nil || len(removed) != 0 {
		t.Fatalf("GC one millisecond before expiry = %v, %v", removed, err)
	}

	removed, err = r.GC(11)
	if err != nil || !sameStrings(removed, []string{"layer-a"}) {
		t.Fatalf("GC = %v, %v; want unchanged deadSince to allow deletion", removed, err)
	}
}

func TestRejectionPriorityAndAtomicity(t *testing.T) {
	r, err := NewReclaimer(-1)
	if err != ErrInvalidRetention {
		t.Fatalf("NewReclaimer error = %v, want ErrInvalidRetention", err)
	}

	r, err = NewReclaimer(10)
	if err != nil {
		t.Fatal(err)
	}

	must(t, r.PutLayer(10, "layer-a", 1))
	must(t, r.PutManifest(11, "manifest-a", []string{"layer-a"}))
	must(t, r.PutIndex(12, "index-a", []string{"manifest-a"}))
	must(t, r.Tag(13, "release", "manifest-a"))

	if err := r.PutLayer(9, "", 1); err != ErrClockSkew {
		t.Fatalf("PutLayer clock error = %v", err)
	}
	if err := r.PutLayer(14, "", 0); err != ErrEmptyDigest {
		t.Fatalf("PutLayer empty digest error = %v", err)
	}
	if err := r.PutLayer(15, "layer-a", 0); err != ErrInvalidSize {
		t.Fatalf("PutLayer size error = %v", err)
	}
	if err := r.PutLayer(16, "layer-a", 2); err != ErrObjectSizeMismatch {
		t.Fatalf("PutLayer size mismatch error = %v", err)
	}
	if err := r.PutLayer(17, "manifest-a", 1); err != ErrObjectKindMismatch {
		t.Fatalf("PutLayer kind mismatch error = %v", err)
	}

	if err := r.PutManifest(18, "manifest-a", nil); err != ErrDigestExists {
		t.Fatalf("PutManifest existing error = %v", err)
	}
	if err := r.PutManifest(19, "new-manifest", nil); err != ErrEmptyReferenceList {
		t.Fatalf("PutManifest empty refs error = %v", err)
	}
	if err := r.PutManifest(20, "new-manifest", []string{"layer-a", "layer-a"}); err != ErrDuplicateReference {
		t.Fatalf("PutManifest duplicate error = %v", err)
	}
	if err := r.PutManifest(21, "new-manifest", []string{"missing", "layer-a"}); err != ErrReferenceNotFound {
		t.Fatalf("PutManifest missing reference error = %v", err)
	}
	if err := r.PutManifest(22, "new-manifest", []string{"layer-a", "index-a"}); err != ErrInvalidReferenceKind {
		t.Fatalf("PutManifest wrong reference kind error = %v", err)
	}

	if err := r.PutIndex(23, "index-a", nil); err != ErrDigestExists {
		t.Fatalf("PutIndex existing error = %v", err)
	}
	if err := r.PutIndex(24, "new-index", []string{"missing", "layer-a"}); err != ErrReferenceNotFound {
		t.Fatalf("PutIndex missing reference error = %v", err)
	}
	if err := r.PutIndex(25, "new-index", []string{"manifest-a", "layer-a"}); err != ErrInvalidReferenceKind {
		t.Fatalf("PutIndex wrong reference kind error = %v", err)
	}

	if err := r.Tag(26, "", "missing"); err != ErrEmptyTagName {
		t.Fatalf("Tag empty name error = %v", err)
	}
	if err := r.Tag(27, "other", "missing"); err != ErrTagTargetNotFound {
		t.Fatalf("Tag missing target error = %v", err)
	}
	if err := r.Tag(28, "other", "layer-a"); err != ErrTagTargetIsLayer {
		t.Fatalf("Tag layer target error = %v", err)
	}

	if err := r.Untag(29, ""); err != ErrEmptyTagName {
		t.Fatalf("Untag empty name error = %v", err)
	}
	if err := r.Untag(30, "missing-tag"); err != ErrTagNotFound {
		t.Fatalf("Untag missing tag error = %v", err)
	}

	if r.maxNow != 13 {
		t.Fatalf("maxNow = %d after rejected operations, want 13", r.maxNow)
	}
}

func TestConcurrentOperations(t *testing.T) {
	r, err := NewReclaimer(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PutLayer(1, "shared-layer", 7); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := r.GC(1000); err != nil {
					t.Error(err)
					return
				}
				if err := r.PutLayer(1000, "shared-layer", 7); err != nil {
					t.Error(err)
					return
				}
				_ = r.Exists("shared-layer")
				_ = r.Live("shared-layer")
			}
		}()
	}
	wg.Wait()

	if !r.Exists("shared-layer") || r.Live("shared-layer") {
		t.Fatalf("concurrent final state wrong: exists=%v live=%v", r.Exists("shared-layer"), r.Live("shared-layer"))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
