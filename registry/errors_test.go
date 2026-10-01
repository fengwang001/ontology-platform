package registry

import (
	"errors"
	"reflect"
	"testing"
)

func TestRejectionErrorsAndPriority(t *testing.T) {
	if _, err := NewReclaimer(-1); !errors.Is(err, ErrInvalidRetention) {
		t.Fatalf("NewReclaimer(-1) error = %v, want ErrInvalidRetention", err)
	}

	r, err := NewReclaimer(10)
	mustDo(t, err, "NewReclaimer")
	mustDo(t, r.PutLayer("layer", 4, 10), "PutLayer")
	mustDo(t, r.PutManifest("manifest", []string{"layer"}, 20), "PutManifest")
	mustDo(t, r.PutIndex("index", []string{"manifest"}, 30), "PutIndex")
	mustDo(t, r.Tag("v1", "manifest", 40), "Tag")

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{"clock before empty digest", func() error { return r.PutLayer("", 1, 5) }, ErrClockMovedBackward},
		{"layer empty digest", func() error { return r.PutLayer("", 1, 50) }, ErrEmptyDigest},
		{"layer invalid size", func() error { return r.PutLayer("new-layer", 0, 51) }, ErrInvalidSize},
		{"layer different kind", func() error { return r.PutLayer("manifest", 9, 52) }, ErrDigestMismatch},
		{"layer different size", func() error { return r.PutLayer("layer", 5, 53) }, ErrDigestMismatch},
		{"manifest existing", func() error { return r.PutManifest("manifest", []string{"layer"}, 54) }, ErrDigestAlreadyExists},
		{"manifest empty refs", func() error { return r.PutManifest("m-empty", nil, 55) }, ErrEmptyReferenceList},
		{"manifest duplicate refs", func() error {
			return r.PutManifest("m-dup", []string{"layer", "layer"}, 56)
		}, ErrDuplicateReference},
		{"manifest missing ref", func() error {
			return r.PutManifest("m-missing", []string{"missing-layer"}, 57)
		}, ErrReferencedDigestMissing},
		{"manifest wrong ref kind", func() error {
			return r.PutManifest("m-wrong", []string{"manifest"}, 58)
		}, ErrInvalidReferenceKind},
		{"index missing ref", func() error {
			return r.PutIndex("i-missing", []string{"missing-manifest"}, 59)
		}, ErrReferencedDigestMissing},
		{"index wrong ref kind", func() error { return r.PutIndex("i-wrong", []string{"layer"}, 60) }, ErrInvalidReferenceKind},
		{"tag empty name", func() error { return r.Tag("", "manifest", 61) }, ErrEmptyTagName},
		{"tag missing target", func() error { return r.Tag("new", "missing", 62) }, ErrTagTargetMissing},
		{"tag layer target", func() error { return r.Tag("layer-tag", "layer", 63) }, ErrTagTargetIsLayer},
		{"untag empty name", func() error { return r.Untag("", 64) }, ErrEmptyTagName},
		{"untag missing", func() error { return r.Untag("missing", 65) }, ErrTagNotFound},
		{"gc clock rollback", func() error { _, err := r.GC(39); return err }, ErrClockMovedBackward},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			t.Logf("input=%s; output=%v; basis=first validation error in the documented priority", tt.name, err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	if r.maxNow != 40 {
		t.Fatalf("maxNow = %d, rejected operations must not advance it beyond 40", r.maxNow)
	}
	if len(r.objects) != 3 || len(r.tags) != 1 {
		t.Fatalf("state after rejected operations = %d objects, %d tags; want 3 objects, 1 tag", len(r.objects), len(r.tags))
	}

	deleted, err := r.GC(66)
	mustDo(t, err, "GC after rejected operations")
	t.Logf("input=GC(66) after all rejected operations; output=%v; basis=only committed state is considered", deleted)
	if !reflect.DeepEqual(deleted, []string{"index"}) {
		t.Fatalf("GC = %v, want [index]", deleted)
	}
}
