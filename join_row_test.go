package ontology

import (
	"reflect"
	"testing"
)

// A right non-key column that shares a name with a left column must not
// overwrite it; it lands under "right.<name>".
func TestSameNameColumnNotOverwritten(t *testing.T) {
	left := []map[string]any{{"k": int64(1), "c": "left-c", "only_l": "L"}}
	right := []map[string]any{{"k": int64(1), "c": "right-c", "only_r": "R"}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 1 {
		t.Fatalf("RowCount = %d, want 1", res.RowCount())
	}
	row := res.Rows[0]
	if row["c"] != "left-c" {
		t.Fatalf("left column c overwritten: %v", row["c"])
	}
	if row["right.c"] != "right-c" {
		t.Fatalf("right column c missing under right.c: %v", row)
	}
	if row["only_l"] != "L" || row["only_r"] != "R" {
		t.Fatalf("non-colliding columns wrong: %v", row)
	}
	wantCols := []string{"only_r", "right.c"}
	if !reflect.DeepEqual(res.RightColumns, wantCols) {
		t.Fatalf("RightColumns = %v, want %v", res.RightColumns, wantCols)
	}
}

// Left-mode unmatched rows carry no right columns at all: missing, not
// zero values, and detectable via IsMissing.
func TestLeftUnmatchedRightSideMissing(t *testing.T) {
	left := []map[string]any{
		{"k": int64(1), "c": "matched"},
		{"k": int64(2), "c": "unmatched"},
	}
	right := []map[string]any{{"k": int64(1), "c": "rc", "extra": 9}}
	res, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res.RowCount() != 2 {
		t.Fatalf("RowCount = %d, want 2", res.RowCount())
	}
	var unmatched map[string]any
	for _, row := range res.Rows {
		if row["c"] == "unmatched" {
			unmatched = row
		}
	}
	if unmatched == nil {
		t.Fatal("unmatched left row missing from output")
	}
	for _, col := range res.RightColumns {
		if !IsMissing(unmatched, col) {
			t.Fatalf("right column %q present on unmatched row: %v", col, unmatched)
		}
	}
	if _, ok := unmatched["extra"]; ok {
		t.Fatal("right-only column leaked into unmatched row")
	}
}

// Mutating a result row must not affect inputs, and mutating inputs after
// the join must not affect results — including nested maps and slices.
func TestResultInputIsolation(t *testing.T) {
	left := []map[string]any{{
		"k": int64(1), "n": map[string]any{"x": "lx"}, "s": []any{"a"},
	}}
	right := []map[string]any{{
		"k": int64(1), "rn": map[string]any{"y": "ry"},
	}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	row := res.Rows[0]
	// Result -> input isolation.
	row["n"].(map[string]any)["x"] = "MUTATED"
	row["s"].([]any)[0] = "MUTATED"
	row["rn"].(map[string]any)["y"] = "MUTATED"
	row["k"] = int64(999)
	if left[0]["n"].(map[string]any)["x"] != "lx" ||
		left[0]["s"].([]any)[0] != "a" ||
		right[0]["rn"].(map[string]any)["y"] != "ry" ||
		left[0]["k"] != int64(1) {
		t.Fatal("mutating result row leaked into inputs")
	}
	// Input -> result isolation.
	left[0]["n"].(map[string]any)["x"] = "INPUT-MUT"
	right[0]["rn"].(map[string]any)["y"] = "INPUT-MUT"
	left[0]["k"] = int64(555)
	row2 := res.Rows[0]
	if row2["n"].(map[string]any)["x"] != "MUTATED" ||
		row2["rn"].(map[string]any)["y"] != "MUTATED" ||
		row2["k"] != int64(999) {
		t.Fatal("mutating inputs leaked into results")
	}
}

// Right key columns do not duplicate into the output row.
func TestRightKeyColumnsNotDuplicated(t *testing.T) {
	left := []map[string]any{{"k": int64(1)}}
	right := []map[string]any{{"k": int64(1), "w": "r"}}
	res, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	row := res.Rows[0]
	if _, ok := row["right.k"]; ok {
		t.Fatal("right key column should not be duplicated as right.k")
	}
	if row["k"] != int64(1) || row["w"] != "r" {
		t.Fatalf("unexpected row: %v", row)
	}
}
