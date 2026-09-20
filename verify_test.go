package ontology

import (
	"errors"
	"testing"
)

func TestVerifyPassesAfterMutations(t *testing.T) {
	s := NewStore("color", "dept")
	for i := 0; i < 100; i++ {
		id := string(rune('A' + i))
		props := map[string]any{"color": []string{"red", "blue"}[i%2]}
		if i%5 == 0 {
			props["dept"] = nil
		} else if i%5 != 1 {
			props["dept"] = "eng"
		}
		s.Upsert(id, props)
	}
	for i := 0; i < 100; i += 3 {
		s.Upsert(string(rune('A'+i)), map[string]any{"color": "green"})
	}
	for i := 0; i < 100; i += 4 {
		s.Delete(string(rune('A' + i)))
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify after mutations: %v", err)
	}
}

func TestVerifyDetectsExtraID(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	// Corrupt: inject a phantom ID into the "red" bucket.
	key, _ := keyOf("red")
	s.indexes["color"].buckets[key]["ghost"] = struct{}{}
	err := s.Verify()
	var verr *VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("Verify error = %v, want *VerifyError", err)
	}
	if verr.Attr != "color" {
		t.Fatalf("Attr = %q, want color", verr.Attr)
	}
	if len(verr.Extra) != 1 || verr.Extra[0] != "ghost" {
		t.Fatalf("Extra = %v, want [ghost]", verr.Extra)
	}
	if len(verr.Missing) != 0 {
		t.Fatalf("Missing = %v, want empty", verr.Missing)
	}
}

func TestVerifyDetectsMissingID(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	s.Upsert("e2", map[string]any{"color": "red"})
	// Corrupt: drop e2 from the bucket and from present.
	key, _ := keyOf("red")
	delete(s.indexes["color"].buckets[key], "e2")
	delete(s.indexes["color"].present, "e2")
	err := s.Verify()
	var verr *VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("Verify error = %v, want *VerifyError", err)
	}
	if len(verr.Missing) != 1 || verr.Missing[0] != "e2" {
		t.Fatalf("Missing = %v, want [e2]", verr.Missing)
	}
	if verr.Value != "red" {
		t.Fatalf("Value = %q, want red", verr.Value)
	}
}

func TestVerifyDetectsNilTrackingDivergence(t *testing.T) {
	s := NewStore("dept")
	s.Upsert("e1", map[string]any{"dept": nil})
	// Corrupt: forget the nil marker.
	delete(s.indexes["dept"].nilIDs, "e1")
	err := s.Verify()
	var verr *VerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("Verify error = %v, want *VerifyError", err)
	}
	if verr.Value != "<nil>" {
		t.Fatalf("Value = %q, want <nil>", verr.Value)
	}
	if len(verr.Missing) != 1 || verr.Missing[0] != "e1" {
		t.Fatalf("Missing = %v, want [e1]", verr.Missing)
	}
}
