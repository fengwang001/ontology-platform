package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func exampleSchema() []Field {
	return []Field{
		{Name: "id", Rep: Required},
		{Name: "author", Rep: Optional, Children: []Field{
			{Name: "name", Rep: Optional},
			{Name: "ids", Rep: Repeated},
		}},
		{Name: "tags", Rep: Repeated, Children: []Field{
			{Name: "k", Rep: Required},
			{Name: "vals", Rep: Repeated},
		}},
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New([]Field{{Name: "g", Rep: Required, Children: []Field{{}}}}, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("empty child name: %v", err)
	}
	if _, err := New(nil, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("no leaves: %v", err)
	}
	if _, err := New([]Field{{Name: "a", Rep: Required}, {Name: "a", Rep: Required}}, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatal("duplicate names")
	}
	if _, err := New(exampleSchema(), 0, 1); !errors.Is(err, ErrParam) {
		t.Fatal("pageEntries=0")
	}
	if _, err := New(exampleSchema(), 1, 0); !errors.Is(err, ErrParam) {
		t.Fatal("maxEntries=0")
	}
	s, err := New(exampleSchema(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "author.name", "author.ids", "tags.k", "tags.vals"}
	if got := s.LeafNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("leaf names: %v", got)
	}
}

func TestRepDefExamples(t *testing.T) {
	s, _ := New(exampleSchema(), 10, 100)
	recs := []map[string]any{
		{"id": int64(1)},
		{"id": int64(2), "author": map[string]any{}},
		{"id": int64(3), "author": map[string]any{"name": int64(7), "ids": []any{int64(5), int64(6)}}},
		{"id": int64(4), "tags": []any{
			map[string]any{"k": int64(1), "vals": []any{int64(10), int64(11)}},
			map[string]any{"k": int64(2)},
		}},
	}
	for _, r := range recs {
		if err := s.Shred(r); err != nil {
			t.Fatal(err)
		}
	}
	check := func(col string, want []Entry) {
		t.Helper()
		got, _ := s.Entries(col)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("col %s\n got %v\nwant %v", col, got, want)
		}
	}
	check("author.ids", []Entry{{0, 0, 0}, {0, 1, 0}, {0, 2, 5}, {1, 2, 6}, {0, 0, 0}})
	check("author.name", []Entry{{0, 0, 0}, {0, 1, 0}, {0, 2, 7}, {0, 0, 0}})
	check("tags.vals", []Entry{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 2, 10}, {2, 2, 11}, {1, 1, 0}})
	check("tags.k", []Entry{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {0, 1, 1}, {1, 1, 2}})

	out, err := s.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		want := naiveNormalize(exampleSchema(), r)
		if !reflect.DeepEqual(out[i], want) {
			t.Fatalf("rec %d\n got %#v\nwant %#v", i, out[i], want)
		}
	}
}
