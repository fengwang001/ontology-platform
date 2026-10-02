package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestEmptyRepeatedAndMissingRep(t *testing.T) {
	schema := []Field{
		{Name: "id", Rep: Required},
		{Name: "a", Rep: Optional, Children: []Field{
			{Name: "r", Rep: Repeated},
			{Name: "q", Rep: Required},
		}},
	}
	s, _ := New(schema, 10, 100)
	must := func(rec map[string]any) {
		t.Helper()
		if err := s.Shred(rec); err != nil {
			t.Fatal(err)
		}
	}
	must(map[string]any{"id": int64(1)}) // a 缺失：r (0,0)，q (0,0)
	must(map[string]any{"id": int64(2), "a": map[string]any{"q": int64(9), "r": []any{}}})
	must(map[string]any{"id": int64(3), "a": map[string]any{"q": int64(8), "r": nil}})

	got, _ := s.Entries("a.r")
	want := []Entry{{0, 0, 0}, {0, 1, 0}, {0, 1, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("r got %v want %v", got, want)
	}
	gotQ, _ := s.Entries("a.q")
	wantQ := []Entry{{0, 0, 0}, {0, 1, 9}, {0, 1, 8}}
	if !reflect.DeepEqual(gotQ, wantQ) {
		t.Fatalf("q got %v want %v", gotQ, wantQ)
	}

	out, err := s.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[0]["a"]; ok {
		t.Fatal("missing optional group must be dropped")
	}
	a, ok := out[1]["a"].(map[string]any)
	if !ok {
		t.Fatal("present optional group must remain")
	}
	if _, hasR := a["r"]; hasR {
		t.Fatal("empty repeated must be dropped")
	}
	if a["q"] != int64(9) {
		t.Fatalf("q = %v", a["q"])
	}
}

func TestNestedRepLevels(t *testing.T) {
	schema := []Field{
		{Name: "id", Rep: Required},
		{Name: "outer", Rep: Repeated, Children: []Field{
			{Name: "inner", Rep: Repeated, Children: []Field{
				{Name: "v", Rep: Required},
			}},
		}},
	}
	s, _ := New(schema, 100, 1000)
	rec := map[string]any{
		"id": int64(1),
		"outer": []any{
			map[string]any{"inner": []any{
				map[string]any{"v": int64(1)},
				map[string]any{"v": int64(2)},
			}},
			map[string]any{"inner": []any{
				map[string]any{"v": int64(3)},
			}},
		},
	}
	if err := s.Shred(rec); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Entries("outer.inner.v")
	want := []Entry{{0, 2, 1}, {2, 2, 2}, {1, 2, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	out, err := s.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out[0], naiveNormalize(schema, rec)) {
		t.Fatalf("roundtrip:\n got %#v\nwant %#v", out[0], naiveNormalize(schema, rec))
	}
}

func TestErrorsAndOrder(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 1000)
	checkErr := func(name string, rec map[string]any, kind error, path string) {
		t.Helper()
		err := s.Shred(rec)
		if !errors.Is(err, kind) {
			t.Fatalf("%s: want %v got %v", name, kind, err)
		}
		if ErrorPath(err) != path {
			t.Fatalf("%s: path want %q got %q", name, path, ErrorPath(err))
		}
	}
	checkErr("missing required", map[string]any{}, ErrMissingRequired, "id")
	checkErr("nil required", map[string]any{"id": nil}, ErrMissingRequired, "id")
	checkErr("leaf type", map[string]any{"id": 7}, ErrType, "id")
	checkErr("group type", map[string]any{"id": int64(1), "author": 3}, ErrType, "author")
	checkErr("repeated type",
		map[string]any{"id": int64(1), "author": map[string]any{"ids": int64(1)}},
		ErrType, "author.ids")
	checkErr("nil elem",
		map[string]any{"id": int64(1), "author": map[string]any{"ids": []any{nil}}},
		ErrType, "author.ids")
	checkErr("group elem",
		map[string]any{"id": int64(1), "tags": []any{5}},
		ErrType, "tags")
	checkErr("unknown",
		map[string]any{"id": int64(1), "zzz": 1, "aaa": 2},
		ErrUnknownField, "aaa")
	checkErr("required inside repeated",
		map[string]any{"id": int64(1), "tags": []any{map[string]any{}}},
		ErrMissingRequired, "tags.k")
	checkErr("order unknown first",
		map[string]any{"nope": 1}, ErrUnknownField, "nope")
	checkErr("order declaration",
		map[string]any{"author": map[string]any{"name": 7}},
		ErrMissingRequired, "id")

	if n := s.Records(); n != 0 {
		t.Fatalf("records changed after rejected shreds: %d", n)
	}
	if es, _ := s.Entries("id"); len(es) != 0 {
		t.Fatalf("entries changed after rejected shreds: %v", es)
	}
	if ps, _ := s.Pages("id"); len(ps) != 0 {
		t.Fatalf("pages changed after rejected shreds: %v", ps)
	}
}

func TestMaxEntriesBoundary(t *testing.T) {
	s, _ := New([]Field{{Name: "v", Rep: Repeated}}, 100, 3)
	if err := s.Shred(map[string]any{"v": []any{int64(1), int64(2), int64(3)}}); err != nil {
		t.Fatalf("equal maxEntries must pass: %v", err)
	}
	err := s.Shred(map[string]any{"v": []any{int64(1), int64(2), int64(3), int64(4)}})
	if !errors.Is(err, ErrTooLarge) || ErrorPath(err) != "v" {
		t.Fatalf("want ErrTooLarge at v, got %v", err)
	}
}
