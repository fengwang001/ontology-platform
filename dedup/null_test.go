package dedup

import "testing"

// TestMissingNilEmptyThreeGroups proves the three kinds of "empty" land
// in three distinct, caller-distinguishable groups.
func TestMissingNilEmptyThreeGroups(t *testing.T) {
	d := New([]string{"a"}, KeepFirst)
	d.Add(map[string]any{"other": 1})  // missing
	d.Add(map[string]any{"a": nil})    // nil
	d.Add(map[string]any{"a": ""})     // empty string
	d.Add(map[string]any{"a": "real"}) // normal
	d.Add(map[string]any{"other": 99}) // missing again
	d.Add(map[string]any{"a": nil})    // nil again
	d.Add(map[string]any{"a": ""})     // empty again
	if got := d.Groups(); got != 4 {
		t.Fatalf("got %d groups, want 4", got)
	}
	if got := d.MissingRows(); got != 2 {
		t.Fatalf("MissingRows = %d, want 2", got)
	}
	if got := d.NilRows(); got != 2 {
		t.Fatalf("NilRows = %d, want 2", got)
	}
	if got := d.EmptyStringRows(); got != 2 {
		t.Fatalf("EmptyStringRows = %d, want 2", got)
	}
	// Each kind must be identifiable in the snapshot.
	var sawMissing, sawNil, sawEmpty bool
	for _, row := range d.Snapshot() {
		v, ok := row["a"]
		switch {
		case !ok:
			sawMissing = true
		case v == nil:
			sawNil = true
		case v == "":
			sawEmpty = true
		}
	}
	if !sawMissing || !sawNil || !sawEmpty {
		t.Fatalf("snapshot must expose all three kinds: missing=%v nil=%v empty=%v",
			sawMissing, sawNil, sawEmpty)
	}
}

// TestMultiColumnEmptyClassification: with several dedup columns, any
// missing column puts the whole row into the single missing group; else
// any nil column into the single nil group; empty strings stay in the
// composite key. Nothing is dropped.
func TestMultiColumnEmptyClassification(t *testing.T) {
	d := New([]string{"a", "b"}, KeepFirst)
	d.Add(map[string]any{"a": int64(1)})          // b missing
	d.Add(map[string]any{"b": int64(2)})          // a missing
	d.Add(map[string]any{"a": nil, "b": "x"})     // nil
	d.Add(map[string]any{"a": "y", "b": nil})     // nil
	d.Add(map[string]any{"a": "", "b": int64(1)}) // empty string
	d.Add(map[string]any{"a": "", "b": int64(1)}) // empty string dup
	d.Add(map[string]any{"a": "", "b": int64(2)}) // empty, other col differs
	d.Add(map[string]any{"a": "v", "b": "w"})     // normal
	// missing(1) + nil(1) + empty(b=1) + empty(b=2) + normal = 5 groups.
	if got := d.Groups(); got != 5 {
		t.Fatalf("got %d groups, want 5", got)
	}
	if got := d.Processed(); got != 8 {
		t.Fatalf("Processed = %d, want 8 (nothing dropped)", got)
	}
	if got := d.MissingRows(); got != 2 {
		t.Fatalf("MissingRows = %d, want 2", got)
	}
	if got := d.NilRows(); got != 2 {
		t.Fatalf("NilRows = %d, want 2", got)
	}
	if got := d.EmptyStringRows(); got != 3 {
		t.Fatalf("EmptyStringRows = %d, want 3", got)
	}
}

// TestEmptyKindsSortBeforeValues pins the deterministic relative order
// of the three empty kinds and ordinary values.
func TestEmptyKindsSortBeforeValues(t *testing.T) {
	d := New([]string{"a"}, KeepFirst)
	d.Add(map[string]any{"a": "z"})
	d.Add(map[string]any{"a": ""})
	d.Add(map[string]any{"a": nil})
	d.Add(map[string]any{"unrelated": true})
	d.Add(map[string]any{"a": int64(5)})
	snap := d.Snapshot()
	if len(snap) != 5 {
		t.Fatalf("got %d rows, want 5", len(snap))
	}
	if _, ok := snap[0]["a"]; ok {
		t.Fatalf("row 0 should be the missing group, got %v", snap[0])
	}
	if snap[1]["a"] != nil {
		t.Fatalf("row 1 should be the nil group, got %v", snap[1])
	}
	if snap[2]["a"] != int64(5) {
		t.Fatalf("row 2 should be the number group, got %v", snap[2])
	}
	if v, ok := snap[3]["a"].(string); !ok || v != "" {
		t.Fatalf("row 3 should be the empty-string group, got %v", snap[3])
	}
	if snap[4]["a"] != "z" {
		t.Fatalf("row 4 should be \"z\", got %v", snap[4])
	}
}
