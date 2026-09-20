package ontology

import (
	"math"
	"testing"
)

// Both sides NULL on the key: Inner must produce nothing, because NULL
// never equals NULL.
func TestInnerNullKeyNeverMatches(t *testing.T) {
	left := []Row{{"id": nil, "v": "L1"}, {"v": "L2"}} // nil key, missing key
	right := []Row{{"id": nil, "w": "R1"}, {"w": "R2"}}

	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("Inner with NULL keys produced %d rows, want 0", len(out))
	}
	if stats.LeftNullKey != 2 {
		t.Fatalf("LeftNullKey = %d, want 2 (nil key and missing key)", stats.LeftNullKey)
	}
	if stats.LeftNoPartner != 0 {
		t.Fatalf("LeftNoPartner = %d, want 0", stats.LeftNoPartner)
	}
}

// Missing key and nil key are both "empty" and must be counted together in
// LeftNullKey, separately from LeftNoPartner.
func TestUnmatchedCountsAreSeparate(t *testing.T) {
	left := []Row{
		{"id": nil, "tag": "nil-key"},
		{"tag": "missing-key"},
		{"id": 7, "tag": "no-partner"},
		{"id": 1, "tag": "matched"},
	}
	right := []Row{{"id": 1, "w": "R"}}

	out, stats, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if stats.LeftNullKey != 2 {
		t.Fatalf("LeftNullKey = %d, want 2", stats.LeftNullKey)
	}
	if stats.LeftNoPartner != 1 {
		t.Fatalf("LeftNoPartner = %d, want 1", stats.LeftNoPartner)
	}
	if stats.OutputRows != 4 { // 1 matched pair + 3 unmatched left rows
		t.Fatalf("OutputRows = %d, want 4", stats.OutputRows)
	}
	if len(out) != 4 {
		t.Fatalf("len(out) = %d, want 4", len(out))
	}
}

// NaN on a key is never equal and counts toward LeftNullKey.
func TestNaNKeyCountsAsNull(t *testing.T) {
	nan := math.NaN()
	left := []Row{{"id": nan, "tag": "nan"}}
	right := []Row{{"id": nan, "w": "R"}}

	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("NaN key matched %d rows, want 0", len(out))
	}
	if stats.LeftNullKey != 1 {
		t.Fatalf("LeftNullKey = %d, want 1 for NaN key", stats.LeftNullKey)
	}
}

// Left mode keeps NULL-keyed left rows as unmatched output rows.
func TestLeftKeepsNullKeyRows(t *testing.T) {
	left := []Row{{"id": nil, "tag": "null-key"}, {"id": 1, "tag": "matched"}}
	right := []Row{{"id": 1, "w": "R"}}

	out, stats, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if stats.OutputRows != 2 || len(out) != 2 {
		t.Fatalf("got %d rows, want 2", len(out))
	}
	// The unmatched row carries the left attributes and no right ones.
	var nullRow Row
	for _, r := range out {
		if r["tag"] == "null-key" {
			nullRow = r
		}
	}
	if nullRow == nil {
		t.Fatal("NULL-key left row missing from Left join output")
	}
	if _, ok := nullRow["w"]; ok {
		t.Fatal("unmatched row must not carry right-side attributes")
	}
}

// A multi-column key is NULL when any one column is empty.
func TestMultiColumnKeyNull(t *testing.T) {
	left := []Row{{"a": 1, "b": nil}, {"a": 1, "b": 2}}
	right := []Row{{"a": 1, "b": 2}}

	out, stats, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1", len(out))
	}
	if stats.LeftNullKey != 1 {
		t.Fatalf("LeftNullKey = %d, want 1", stats.LeftNullKey)
	}
}
