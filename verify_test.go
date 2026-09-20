package ontology

import (
	"strings"
	"testing"
)

func TestVerifyPassesAfterMutations(t *testing.T) {
	s := New("color", "size")
	for i := 0; i < 50; i++ {
		id := string(rune('a' + i))
		s.Upsert(id, map[string]any{"color": "red", "size": i})
		if err := s.Verify(); err != nil {
			t.Fatalf("Verify after upsert %d: %v", i, err)
		}
	}
	for i := 0; i < 50; i += 2 {
		id := string(rune('a' + i))
		s.Upsert(id, map[string]any{"color": "blue"})
		if err := s.Verify(); err != nil {
			t.Fatalf("Verify after update %d: %v", i, err)
		}
	}
	for i := 0; i < 50; i += 3 {
		s.Delete(string(rune('a' + i)))
		if err := s.Verify(); err != nil {
			t.Fatalf("Verify after delete %d: %v", i, err)
		}
	}
}

func TestVerifyDetectsStaleEntry(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	// Sabotage: inject a phantom ID into the index.
	s.indexes["color"][keyOf("red")]["ghost"] = struct{}{}
	err := s.Verify()
	if err == nil {
		t.Fatal("Verify must fail on a stale index entry")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"color"`) || !strings.Contains(msg, "ghost") ||
		!strings.Contains(msg, "stale") {
		t.Fatalf("error must locate attr and stale ID, got: %v", err)
	}
}

func TestVerifyDetectsMissingEntry(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": "red"})
	// Sabotage: drop a real ID from the index.
	delete(s.indexes["color"][keyOf("red")], "b")
	err := s.Verify()
	if err == nil {
		t.Fatal("Verify must fail on a missing index entry")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"color"`) || !strings.Contains(msg, "b") ||
		!strings.Contains(msg, "missing") {
		t.Fatalf("error must locate attr and missing ID, got: %v", err)
	}
}

func TestVerifyDetectsPhantomValue(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	// Sabotage: add an entire phantom value bucket.
	s.indexes["color"][keyOf("green")] = map[string]struct{}{"a": {}}
	if err := s.Verify(); err == nil {
		t.Fatal("Verify must fail on a phantom value bucket")
	}
}
