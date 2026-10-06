package layerconfig_test

import (
	"testing"

	"ontology/layerconfig"
)

// TestPublishAfterRollback verifies version numbering and history indexing
// stay coherent when publishes continue after a rollback.
func TestPublishAfterRollback(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	mustRegister(t, s, layerconfig.Schema{
		Key: "a", Type: layerconfig.TypeString, Required: true, Merge: layerconfig.MergeOverride,
	}, l)

	for _, v := range []string{"v1", "v2", "v3", "v4"} {
		mustPublish(t, s, l, "advance to "+v,
			layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "a", Value: layerconfig.Value{Str: v}},
		)
	}
	nv, err := s.Rollback(2)
	l.logRollback(2, nv, err, "rollback to v2 content")
	if err != nil || nv != 5 {
		t.Fatalf("rollback version: got %d err=%v want 5", nv, err)
	}
	nv2 := mustPublish(t, s, l, "publish after rollback must be v6",
		layerconfig.Change{Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "a", Value: layerconfig.Value{Str: "v6"}},
	)
	if nv2 != 6 {
		t.Fatalf("post-rollback publish version: got %d want 6", nv2)
	}

	wantByVersion := map[int]string{0: "", 1: "v1", 2: "v2", 3: "v3", 4: "v4", 5: "v2", 6: "v6"}
	for v := 0; v <= 6; v++ {
		r, err := s.Resolve(v, layerconfig.Scope{}, "a")
		if err != nil {
			t.Fatalf("resolve v%d: %v", v, err)
		}
		if v == 0 {
			if r.Present {
				t.Fatalf("v0 must be unset")
			}
			continue
		}
		if !r.Present || r.Value.Str != wantByVersion[v] {
			t.Fatalf("v%d: got %+v want %q", v, r, wantByVersion[v])
		}
	}
	if _, err := s.Resolve(7, layerconfig.Scope{}, "a"); kindOf(err) != layerconfig.KindVersionNotFound {
		t.Fatalf("future version: want VersionNotFound got %v", err)
	}
	l.line("post-rollback chain verified: versions 0..6 content correct, v7 -> VersionNotFound")
}
