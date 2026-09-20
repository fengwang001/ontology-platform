package ontology

import (
	"testing"
)

// A same-named non-key attribute on both sides must not be overwritten by
// the right row; the right value lands under "right."+name.
func TestNonKeyCollisionNotOverwritten(t *testing.T) {
	left := []Row{{"id": 1, "name": "left-name", "onlyLeft": true}}
	right := []Row{{"id": 1, "name": "right-name", "onlyRight": true}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	r := out[0]
	if r["name"] != "left-name" {
		t.Fatalf("name = %v, want left value kept", r["name"])
	}
	if r["right.name"] != "right-name" {
		t.Fatalf("right.name = %v, want right value under renamed key", r["right.name"])
	}
	if r["onlyLeft"] != true || r["onlyRight"] != true {
		t.Fatalf("one-sided attributes lost: %v", r)
	}
	if r["id"] != 1 {
		t.Fatalf("join key id = %v, want 1", r["id"])
	}
}

// In Left mode an unmatched row has no right-side attributes at all: they
// are missing (comma-ok detectable), never zero values.
func TestLeftUnmatchedRightSideMissing(t *testing.T) {
	left := []Row{{"id": 1, "name": "L"}}
	right := []Row{{"id": 2, "name": "R", "extra": 9}}
	out, _, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	r := out[0]
	if _, ok := r["right.name"]; ok {
		t.Fatal("unmatched row must not carry right.name")
	}
	if _, ok := r["extra"]; ok {
		t.Fatal("unmatched row must not carry right-only attribute extra")
	}
	if _, ok := r["name"]; !ok {
		t.Fatal("unmatched row must keep its left attributes")
	}
}

// Mutating a result row must not affect the input rows, and mutating the
// input afterwards must not affect already produced results.
func TestResultAndInputAreIsolated(t *testing.T) {
	left := []Row{{
		"id":     1,
		"nested": map[string]any{"x": 1},
		"list":   []any{1, 2},
	}}
	right := []Row{{
		"id":      1,
		"rnested": map[string]any{"y": 2},
	}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	// Result -> input isolation, including nested structures.
	out[0]["id"] = 999
	out[0]["nested"].(map[string]any)["x"] = 111
	out[0]["list"].([]any)[0] = 777
	out[0]["rnested"].(map[string]any)["y"] = 888
	if left[0]["id"] != 1 {
		t.Fatal("mutating result changed input left row")
	}
	if left[0]["nested"].(map[string]any)["x"] != 1 {
		t.Fatal("mutating result nested map changed input")
	}
	if left[0]["list"].([]any)[0] != 1 {
		t.Fatal("mutating result slice changed input")
	}
	if right[0]["rnested"].(map[string]any)["y"] != 2 {
		t.Fatal("mutating result changed input right row")
	}
	// Input -> result isolation.
	left[0]["id"] = 555
	left[0]["nested"].(map[string]any)["x"] = 444
	right[0]["rnested"].(map[string]any)["y"] = 333
	if out[0]["id"] != 999 {
		t.Fatal("mutating input changed result row")
	}
	if out[0]["nested"].(map[string]any)["x"] != 111 {
		t.Fatal("mutating input nested map changed result")
	}
	if out[0]["rnested"].(map[string]any)["y"] != 888 {
		t.Fatal("mutating input changed result right-side value")
	}
}

// The join key value in the result comes from the left row, even when the
// right row stored it in a different numeric form.
func TestJoinKeyTakenFromLeft(t *testing.T) {
	left := []Row{{"id": int64(5)}}
	right := []Row{{"id": 5.0}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if v, ok := out[0]["id"].(int64); !ok || v != 5 {
		t.Fatalf("id = %v (%T), want int64(5) from the left row", out[0]["id"], out[0]["id"])
	}
}
