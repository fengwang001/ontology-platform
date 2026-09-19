package aggregate

import "testing"

func kindOf(res []Result, name string, want MissingKind) *Result {
	for i := range res {
		cols := res[i].Key.Columns
		if len(cols) != 1 || cols[0].Name != name {
			continue
		}
		if cols[0].Kind == want {
			return &res[i]
		}
	}
	return nil
}

func TestThreeMissingSituationsAreDistinctGroups(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	rows := []map[string]any{
		{"v": int64(1)},           // k absent
		{"k": nil, "v": int64(2)}, // k null
		{"k": "", "v": int64(3)},  // k empty string
		{"k": "x", "v": int64(4)}, // k present
	}
	for _, r := range rows {
		agg.Add(r)
	}
	res, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("want 4 distinct groups, got %d: %+v", len(res), res)
	}
	for _, want := range []MissingKind{Absent, Null, EmptyString, Present} {
		if kindOf(res, "k", want) == nil {
			t.Errorf("missing group for kind %d", want)
		}
	}

	absent := kindOf(res, "k", Absent)
	null := kindOf(res, "k", Null)
	empty := kindOf(res, "k", EmptyString)
	if absent.Count != 1 || null.Count != 1 || empty.Count != 1 {
		t.Errorf("each missing group must have exactly one row: %d %d %d",
			absent.Count, null.Count, empty.Count)
	}
	if absent.Key.Columns[0].Missing != Absent ||
		null.Key.Columns[0].Missing != Null ||
		empty.Key.Columns[0].Missing != EmptyString {
		t.Errorf("caller-identifiable missing kinds wrong")
	}
}

func TestMultiColumnMissingRouting(t *testing.T) {
	agg := NewAggregator([]string{"a", "b"}, "v")
	// present/present, absent/present, null/present, present/absent.
	rows := []map[string]any{
		{"a": "x", "b": "y", "v": int64(1)},
		{"b": "y", "v": int64(2)},
		{"a": nil, "b": "y", "v": int64(3)},
		{"a": "x", "v": int64(4)},
	}
	for _, r := range rows {
		agg.Add(r)
	}
	res, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("want 4 groups, got %d", len(res))
	}
	find := func(ka, kb MissingKind) *Result {
		for i := range res {
			c := res[i].Key.Columns
			if c[0].Kind == ka && c[1].Kind == kb {
				return &res[i]
			}
		}
		return nil
	}
	if find(Absent, Present) == nil || find(Null, Present) == nil ||
		find(Present, Absent) == nil || find(Present, Present) == nil {
		t.Fatalf("expected routed groups not found: %+v", res)
	}
}

func TestTypeCollisionDoesNotMergeGroups(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "1", "v": int64(1)})
	agg.Add(map[string]any{"k": int64(1), "v": int64(1)})
	res, _ := agg.Snapshot()
	if len(res) != 2 {
		t.Fatalf("string \"1\" and int64 1 must be different groups, got %d", len(res))
	}
}
