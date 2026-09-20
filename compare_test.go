package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestCrossTypeErrorIdentifiesKeyAndTypes(t *testing.T) {
	rows := []map[string]any{
		{"v": "hello"},
		{"v": int64(42)},
	}
	_, err := New(SortKey{Field: "v"}).Sort(rows)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var inc *IncomparableError
	if !errors.As(err, &inc) {
		t.Fatalf("error type = %T, want *IncomparableError", err)
	}
	if inc.Key != "v" {
		t.Fatalf("Key = %q, want %q", inc.Key, "v")
	}
	got := []string{inc.TypeA, inc.TypeB}
	want := []string{"int64", "string"}
	if inc.TypeA > inc.TypeB {
		got[0], got[1] = got[1], got[0]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
}

func TestCrossTypeErrorOnSecondKey(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(1), "b": "x"},
		{"a": int64(1), "b": true},
	}
	_, err := New(SortKey{Field: "a"}, SortKey{Field: "b"}).Sort(rows)
	var inc *IncomparableError
	if !errors.As(err, &inc) {
		t.Fatalf("error = %v, want *IncomparableError", err)
	}
	if inc.Key != "b" || inc.KeyIndex != 1 {
		t.Fatalf("got key %q index %d, want \"b\" index 1", inc.Key, inc.KeyIndex)
	}
}

func TestInt64AndFloat64CompareNumerically(t *testing.T) {
	rows := []map[string]any{
		{"v": int64(2)},
		{"v": 1.5},
		{"v": int64(1)},
		{"v": 2.5},
	}
	res, err := New(SortKey{Field: "v"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if !reflect.DeepEqual(res.Indices, []int{2, 1, 0, 3}) {
		t.Fatalf("Indices = %v, want [2 1 0 3]", res.Indices)
	}
	// Descending negates values only.
	desc, err := New(SortKey{Field: "v", Desc: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort desc: %v", err)
	}
	if !reflect.DeepEqual(desc.Indices, []int{3, 0, 1, 2}) {
		t.Fatalf("desc Indices = %v, want [3 0 1 2]", desc.Indices)
	}
}

func TestUnsupportedTypeIsErrorNotPanic(t *testing.T) {
	rows := []map[string]any{
		{"v": []int{1}},
		{"v": []int{2}},
	}
	_, err := New(SortKey{Field: "v"}).Sort(rows)
	var inc *IncomparableError
	if !errors.As(err, &inc) {
		t.Fatalf("error = %v, want *IncomparableError", err)
	}
}

func TestBoolAndStringKeys(t *testing.T) {
	rows := []map[string]any{
		{"b": true, "s": "b"},
		{"b": false, "s": "a"},
	}
	res, err := New(SortKey{Field: "b"}, SortKey{Field: "s"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if !reflect.DeepEqual(res.Indices, []int{1, 0}) {
		t.Fatalf("Indices = %v, want [1 0]", res.Indices)
	}
}
