package ontology

import "testing"

// The three missing situations must land in three distinct,
// caller-identifiable groups.
func TestMissingKindsAreDistinctGroups(t *testing.T) {
	agg := NewAggregator([]string{"region"}, "v")
	agg.Add(map[string]any{"v": int64(1)})                // absent
	agg.Add(map[string]any{"region": nil, "v": int64(2)}) // nil
	agg.Add(map[string]any{"region": "", "v": int64(3)})  // empty string
	agg.Add(map[string]any{"region": "us", "v": int64(4)})

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d groups, want 4", len(results))
	}
	byKind := map[PartKind]GroupResult{}
	for _, r := range results {
		byKind[r.Key[0].Kind] = r
	}
	for _, kind := range []PartKind{PartAbsent, PartNil, PartEmpty, PartValue} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("missing group for kind %d", kind)
		}
	}
	if byKind[PartAbsent].IntSum != 1 || byKind[PartNil].IntSum != 2 ||
		byKind[PartEmpty].IntSum != 3 || byKind[PartValue].IntSum != 4 {
		t.Fatalf("groups merged or misassigned: %+v", byKind)
	}
}

// With a multi-column key, any single missing column routes the row to
// the corresponding missing group; the row is never dropped.
func TestMultiColumnMissingRouting(t *testing.T) {
	agg := NewAggregator([]string{"a", "b"}, "v")
	agg.Add(map[string]any{"a": "x", "b": "y", "v": int64(1)})
	agg.Add(map[string]any{"b": "y", "v": int64(2)})           // a absent
	agg.Add(map[string]any{"a": "x", "v": int64(3)})           // b absent
	agg.Add(map[string]any{"a": nil, "b": "y", "v": int64(4)}) // a nil
	agg.Add(map[string]any{"a": "x", "b": "", "v": int64(5)})  // b empty

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("got %d groups, want 5: %+v", len(results), results)
	}
	var total int64
	for _, r := range results {
		total += r.Count
	}
	if total != 5 {
		t.Fatalf("rows dropped: total count %d, want 5", total)
	}
	// Spot-check the (absent, y) and (x, empty) groups.
	found := map[string]int64{}
	for _, r := range results {
		found[KeyString(r.Key)] = r.IntSum
	}
	if found["(<absent>, y)"] != 2 || found["(x, <absent>)"] != 3 ||
		found["(<nil>, y)"] != 4 || found["(x, <empty>)"] != 5 {
		t.Fatalf("misrouted groups: %v", found)
	}
}

// Sorting: absent < nil < empty < present values, values ascending.
func TestKeyOrdering(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	rows := []map[string]any{
		{"k": "b", "v": int64(1)},
		{"k": nil, "v": int64(1)},
		{"k": "a", "v": int64(1)},
		{"v": int64(1)},
		{"k": "", "v": int64(1)},
	}
	for _, row := range rows {
		agg.Add(row)
	}
	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"(<absent>)", "(<nil>)", "(<empty>)", "(a)", "(b)"}
	if len(results) != len(want) {
		t.Fatalf("got %d groups, want %d", len(results), len(want))
	}
	for i, r := range results {
		if got := KeyString(r.Key); got != want[i] {
			t.Fatalf("position %d: got %s, want %s", i, got, want[i])
		}
	}
}

// Distinct types with the same printed form must not collide.
func TestKeyTypeTagsPreventCollision(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "1", "v": int64(1)})
	agg.Add(map[string]any{"k": int64(1), "v": int64(1)})
	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("string \"1\" and int64 1 collided: %+v", results)
	}
}
