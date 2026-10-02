package ontology

import (
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

func TestSmokeExample(t *testing.T) {
	s, err := New(exampleSchema(), 3, 64)
	if err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"id": int64(1),
		"author": map[string]any{
			"ids": []any{int64(5), int64(6)},
		},
		"tags": []any{
			map[string]any{"k": int64(1), "vals": []any{int64(10), int64(11)}},
			map[string]any{"k": int64(2)},
		},
	}
	if err := s.Shred(rec); err != nil {
		t.Fatal(err)
	}
	want := map[string][]Entry{
		"id":          {{0, 0, false, 1}},
		"author.name": {{0, 1, true, 0}},
		"author.ids":  {{0, 2, false, 5}, {1, 2, false, 6}},
		"tags.k":      {{0, 1, false, 1}, {1, 1, false, 2}},
		"tags.vals":   {{0, 2, false, 10}, {2, 2, false, 11}, {1, 1, true, 0}},
	}
	for col, es := range want {
		if got := s.Entries(col); !reflect.DeepEqual(got, es) {
			t.Errorf("col %s = %+v, want %+v", col, got, es)
		}
	}
	got := s.Assemble()
	norm := map[string]any{
		"id": int64(1),
		"author": map[string]any{
			"ids": []any{int64(5), int64(6)},
		},
		"tags": []any{
			map[string]any{"k": int64(1), "vals": []any{int64(10), int64(11)}},
			map[string]any{"k": int64(2)},
		},
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], norm) {
		t.Fatalf("assembled %#v", got)
	}
}
