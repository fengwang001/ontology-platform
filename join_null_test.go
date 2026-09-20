package ontology

import (
	"math"
	"testing"
)

// Both sides nil on the key: Inner must not match them, and the nil-key
// left rows must be counted as null-key unmatched, not as key-unmatched.
func TestInnerNilKeysNeverMatch(t *testing.T) {
	left := []map[string]any{
		{"k": nil, "v": "l1"},
		{"k": nil, "v": "l2"},
	}
	right := []map[string]any{
		{"k": nil, "w": "r1"},
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 0 {
		t.Fatalf("Inner with nil keys on both sides: got %d rows, want 0", res.RowCount())
	}
	if res.LeftNullKeyCount() != 2 {
		t.Fatalf("LeftNullKeyCount = %d, want 2", res.LeftNullKeyCount())
	}
	if res.LeftUnmatchedCount() != 0 {
		t.Fatalf("LeftUnmatchedCount = %d, want 0", res.LeftUnmatchedCount())
	}
}

// A missing key column and an explicit nil are both empty keys.
func TestMissingAndNilKeysAreBothEmpty(t *testing.T) {
	left := []map[string]any{
		{"v": "no-key"},        // key column absent
		{"k": nil, "v": "nil"}, // key column nil
	}
	right := []map[string]any{
		{"k": "no-key"}, // would "match" the first row's absence if absence were a value
	}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 0 {
		t.Fatalf("got %d rows, want 0", res.RowCount())
	}
	if res.LeftNullKeyCount() != 2 {
		t.Fatalf("LeftNullKeyCount = %d, want 2", res.LeftNullKeyCount())
	}
}

// In Left mode, empty-key left rows are still emitted, with no right side.
func TestLeftModeKeepsNullKeyRows(t *testing.T) {
	left := []map[string]any{
		{"k": nil, "v": "null-key"},
		{"k": "x", "v": "matched"},
	}
	right := []map[string]any{
		{"k": "x", "w": "r"},
	}
	res, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 2 {
		t.Fatalf("RowCount = %d, want 2", res.RowCount())
	}
	var nullRow map[string]any
	for _, row := range res.Rows {
		if row["v"] == "null-key" {
			nullRow = row
		}
	}
	if nullRow == nil {
		t.Fatal("null-key left row missing from Left output")
	}
	if !IsMissing(nullRow, "w") {
		t.Fatal("right column w should be missing for unmatched null-key row")
	}
}

// The two unmatched counters stay separate: empty keys vs. no right match.
func TestUnmatchedCountsAreSeparate(t *testing.T) {
	left := []map[string]any{
		{"k": "a", "v": 1}, // right has no "a": key-unmatched
		{"k": "b", "v": 2}, // right has no "b": key-unmatched
		{"k": nil, "v": 3}, // null-key unmatched
		{"v": 4},           // missing key: null-key unmatched
		{"k": "c", "v": 5}, // matches
	}
	right := []map[string]any{{"k": "c"}}
	res, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.LeftNullKeyCount() != 2 {
		t.Fatalf("LeftNullKeyCount = %d, want 2", res.LeftNullKeyCount())
	}
	if res.LeftUnmatchedCount() != 2 {
		t.Fatalf("LeftUnmatchedCount = %d, want 2", res.LeftUnmatchedCount())
	}
	if res.RowCount() != 5 { // 1 matched pair + 4 unmatched left rows
		t.Fatalf("RowCount = %d, want 5", res.RowCount())
	}
}

// NaN keys never match and count as null-key unmatched.
func TestNaNKeyNeverMatches(t *testing.T) {
	nan := math.NaN()
	left := []map[string]any{{"k": nan, "v": "l"}}
	right := []map[string]any{{"k": nan, "w": "r"}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 0 {
		t.Fatalf("NaN key matched: got %d rows, want 0", res.RowCount())
	}
	if res.LeftNullKeyCount() != 1 {
		t.Fatalf("LeftNullKeyCount = %d, want 1 (NaN counts as empty key)", res.LeftNullKeyCount())
	}
	if res.LeftUnmatchedCount() != 0 {
		t.Fatalf("LeftUnmatchedCount = %d, want 0", res.LeftUnmatchedCount())
	}
}

// Multi-column keys: NULL in any one column makes the whole key empty.
func TestCompositeKeyNullInAnyColumn(t *testing.T) {
	left := []map[string]any{
		{"a": "x", "b": nil},
		{"a": "x", "b": "y"},
	}
	right := []map[string]any{
		{"a": "x", "b": nil},
		{"a": "x", "b": "y"},
	}
	res, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1 (only the fully-keyed pair)", res.RowCount())
	}
	if res.LeftNullKeyCount() != 1 {
		t.Fatalf("LeftNullKeyCount = %d, want 1", res.LeftNullKeyCount())
	}
}
