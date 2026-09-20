package ontology

import (
	"reflect"
	"testing"
)

// equalRows builds n rows that are identical on the given keys but
// carry a distinct tag so their identity is observable.
func equalRows(n int, key string) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{key: "same", "tag": i}
	}
	return rows
}

func TestStableAllKeysEqual(t *testing.T) {
	rows := equalRows(64, "k")
	res, err := New(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	for i, idx := range res.Indices {
		if idx != i {
			t.Fatalf("Indices not identity at %d: got %v", i, res.Indices)
		}
		if i > 0 && res.Indices[i] <= res.Indices[i-1] {
			t.Fatalf("Indices not strictly increasing: %v", res.Indices)
		}
	}
}

func TestDescDoesNotReverseEqualRows(t *testing.T) {
	rows := equalRows(32, "k")
	asc, err := New(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort asc: %v", err)
	}
	desc, err := New(SortKey{Field: "k", Desc: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort desc: %v", err)
	}
	if !reflect.DeepEqual(asc.Indices, desc.Indices) {
		t.Fatalf("equal rows reordered by Desc: asc=%v desc=%v",
			asc.Indices, desc.Indices)
	}
	for i, idx := range desc.Indices {
		if desc.Rows[i]["tag"] != idx {
			t.Fatalf("row/tag mismatch at %d", i)
		}
	}
}

func TestInputNotModified(t *testing.T) {
	rows := []map[string]any{
		{"k": int64(3), "s": "c"},
		{"k": int64(1), "s": "a"},
		{"k": int64(2), "s": "b"},
	}
	snapshot := make([]map[string]any, len(rows))
	for i, r := range rows {
		cp := make(map[string]any, len(r))
		for k, v := range r {
			cp[k] = v
		}
		snapshot[i] = cp
	}
	res, err := New(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Rows[0]["k"]; got != int64(1) {
		t.Fatalf("unexpected first row: %v", got)
	}
	for i := range rows {
		if !reflect.DeepEqual(rows[i], snapshot[i]) {
			t.Fatalf("row %d mutated: %v vs %v", i, rows[i], snapshot[i])
		}
	}
	if &res.Rows[0] == &rows[0] {
		t.Fatal("Sort returned the input slice, want a new one")
	}
}

func TestShortCircuitComparisonCount(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(5), "b": "x", "c": true},
		{"a": int64(1), "b": "y", "c": false},
		{"a": int64(9), "b": "z", "c": true},
		{"a": int64(3), "b": "w", "c": false},
	}
	one, err := New(SortKey{Field: "a"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort 1 key: %v", err)
	}
	four, err := New(
		SortKey{Field: "a"},
		SortKey{Field: "b"},
		SortKey{Field: "c"},
		SortKey{Field: "b", Desc: true},
	).Sort(rows)
	if err != nil {
		t.Fatalf("Sort 4 keys: %v", err)
	}
	if one.Comparisons == 0 {
		t.Fatal("expected positive comparison count")
	}
	if one.Comparisons != four.Comparisons {
		t.Fatalf("comparisons grew with trailing keys: 1 key=%d, 4 keys=%d",
			one.Comparisons, four.Comparisons)
	}
}

func TestMultiKeyOrdering(t *testing.T) {
	rows := []map[string]any{
		{"a": "x", "b": int64(2)},
		{"a": "x", "b": int64(1)},
		{"a": "w", "b": int64(9)},
	}
	res, err := New(SortKey{Field: "a"}, SortKey{Field: "b", Desc: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	want := []int{2, 0, 1}
	if !reflect.DeepEqual(res.Indices, want) {
		t.Fatalf("Indices = %v, want %v", res.Indices, want)
	}
}
