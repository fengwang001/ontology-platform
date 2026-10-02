package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestSchemaValidation(t *testing.T) {
	cases := []struct {
		name   string
		fields []Field
	}{
		{"no leaf", []Field{}},
		{"empty name", []Field{{Name: "", Rep: Required}}},
		{"dup name", []Field{{Name: "a", Rep: Required}, {Name: "a", Rep: Required}}},
		{"bad rep", []Field{{Name: "a", Rep: Rep(9)}}},
		{"group at depth 6", depth6Schema(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.fields, 1, 1); !errors.Is(err, ErrSchema) {
				t.Fatalf("want ErrSchema, got %v", err)
			}
		})
	}
	if _, err := New(depth6Schema(false), 1, 1); err != nil {
		t.Fatalf("depth-6 leaf should be allowed: %v", err)
	}
	var many []Field
	for i := 0; i < 17; i++ {
		many = append(many, Field{Name: fmt.Sprintf("f%d", i), Rep: Required})
	}
	if _, err := New(many, 1, 1); !errors.Is(err, ErrSchema) {
		t.Fatalf("17 leaves: want ErrSchema, got %v", err)
	}
	if _, err := New([]Field{{Name: "a", Rep: Required}}, 0, 1); !errors.Is(err, ErrParam) {
		t.Fatalf("pageEntries=0: %v", err)
	}
	if _, err := New([]Field{{Name: "a", Rep: Required}}, 1, 0); !errors.Is(err, ErrParam) {
		t.Fatalf("maxEntries=0: %v", err)
	}
}

func depth6Schema(groupAtSix bool) []Field {
	root := Field{Name: "g1", Rep: Optional}
	cur := &root
	for i := 2; i <= 5; i++ {
		cur.Children = []Field{{Name: fmt.Sprintf("g%d", i), Rep: Optional}}
		cur = &cur.Children[0]
	}
	if groupAtSix {
		cur.Children = []Field{{Name: "g6", Rep: Optional, Children: []Field{{Name: "x", Rep: Optional}}}}
	} else {
		cur.Children = []Field{{Name: "x", Rep: Optional}}
	}
	return []Field{root}
}

func TestDefMissingOptionalGroupVsPresentEmpty(t *testing.T) {
	s, _ := New(exampleSchema(), 10, 100)
	if err := s.Shred(map[string]any{"id": int64(1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shred(map[string]any{"id": int64(2), "author": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	wantName := []Entry{{0, 0, true, 0}, {0, 1, true, 0}}
	if got := s.Entries("author.name"); !reflect.DeepEqual(got, wantName) {
		t.Fatalf("author.name = %+v", got)
	}
	wantIds := []Entry{{0, 0, true, 0}, {0, 1, true, 0}}
	if got := s.Entries("author.ids"); !reflect.DeepEqual(got, wantIds) {
		t.Fatalf("author.ids = %+v", got)
	}
	got := s.Assemble()
	if _, ok := got[0]["author"]; ok {
		t.Fatalf("record0 author should be absent: %#v", got[0])
	}
	a1, ok := got[1]["author"].(map[string]any)
	if !ok || len(a1) != 0 {
		t.Fatalf("record1 author should be empty map: %#v", got[1])
	}
}

func TestEmptyRepeatedVsMissingRep(t *testing.T) {
	s, _ := New(exampleSchema(), 10, 100)
	s.Shred(map[string]any{"id": int64(1)})
	s.Shred(map[string]any{"id": int64(2), "tags": []any{}})
	s.Shred(map[string]any{"id": int64(3), "author": map[string]any{"ids": []any{}}})
	vals := s.Entries("tags.vals")
	for i, want := range []Entry{{0, 0, true, 0}, {0, 0, true, 0}} {
		if vals[i] != want {
			t.Fatalf("tags.vals[%d]=%+v want %+v", i, vals[i], want)
		}
	}
	if id := s.Entries("author.ids")[2]; id != (Entry{0, 1, true, 0}) {
		t.Fatalf("author.ids rec2 = %+v", id)
	}
	got := s.Assemble()
	if _, ok := got[2]["author"].(map[string]any)["ids"]; ok {
		t.Fatalf("empty repeated should be normalized away: %#v", got[2])
	}
}

func TestNestedRepLevels(t *testing.T) {
	schema := []Field{{Name: "a", Rep: Repeated, Children: []Field{
		{Name: "b", Rep: Repeated},
	}}}
	s, _ := New(schema, 100, 100)
	rec := map[string]any{"a": []any{
		map[string]any{"b": []any{int64(1), int64(2)}},
		map[string]any{"b": []any{int64(3)}},
	}}
	if err := s.Shred(rec); err != nil {
		t.Fatal(err)
	}
	want := []Entry{{0, 2, false, 1}, {2, 2, false, 2}, {1, 2, false, 3}}
	if got := s.Entries("a.b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	got := s.Assemble()
	if !reflect.DeepEqual(got[0], rec) {
		t.Fatalf("%#v", got[0])
	}
}

func TestRequiredChildMissingInOptionalGroup(t *testing.T) {
	schema := []Field{
		{Name: "o", Rep: Optional, Children: []Field{{Name: "r", Rep: Required}}},
	}
	s, _ := New(schema, 10, 100)
	err := s.Shred(map[string]any{"o": map[string]any{}})
	if !errors.Is(err, ErrMissingRequired) || ErrorPath(err) != "o.r" {
		t.Fatalf("got %v path %q", err, ErrorPath(err))
	}
	err = s.Shred(map[string]any{"o": map[string]any{"r": nil}})
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("nil required: %v", err)
	}
	if err := s.Shred(map[string]any{}); err != nil {
		t.Fatalf("missing optional group: %v", err)
	}
}

func TestErrorOrderAndPaths(t *testing.T) {
	s, _ := New(exampleSchema(), 10, 100)

	err := s.Shred(map[string]any{"id": int64(1), "z": 1, "a": 1})
	if !errors.Is(err, ErrUnknownField) || ErrorPath(err) != "a" {
		t.Fatalf("unknown: %v %q", err, ErrorPath(err))
	}

	err = s.Shred(map[string]any{"author": map[string]any{}})
	if !errors.Is(err, ErrMissingRequired) || ErrorPath(err) != "id" {
		t.Fatalf("id missing: %v", err)
	}

	err = s.Shred(map[string]any{"id": 1})
	if !errors.Is(err, ErrType) || ErrorPath(err) != "id" {
		t.Fatalf("id type: %v", err)
	}

	err = s.Shred(map[string]any{"id": int64(1), "tags": []int64{1}})
	if !errors.Is(err, ErrType) || ErrorPath(err) != "tags" {
		t.Fatalf("tags type: %v", err)
	}

	err = s.Shred(map[string]any{"id": int64(1), "author": map[string]any{
		"ids": []any{nil},
	}})
	if !errors.Is(err, ErrType) || ErrorPath(err) != "author.ids.0" {
		t.Fatalf("nil elem: %v %q", err, ErrorPath(err))
	}

	err = s.Shred(map[string]any{"id": int64(1), "tags": []any{int64(7)}})
	if !errors.Is(err, ErrType) || ErrorPath(err) != "tags.0" {
		t.Fatalf("tags elem: %v %q", err, ErrorPath(err))
	}

	err = s.Shred(map[string]any{"id": int64(1), "tags": []any{
		map[string]any{"k": int64(1), "q": int64(0)},
	}})
	if !errors.Is(err, ErrUnknownField) || ErrorPath(err) != "tags.0.q" {
		t.Fatalf("nested unknown: %v %q", err, ErrorPath(err))
	}

	err = s.Shred(map[string]any{"id": int64(1), "tags": []any{
		map[string]any{"k": int64(1)},
		map[string]any{},
	}})
	if !errors.Is(err, ErrMissingRequired) || ErrorPath(err) != "tags.1.k" {
		t.Fatalf("nested required: %v %q", err, ErrorPath(err))
	}

	if n := s.RecordCount(); n != 0 {
		t.Fatalf("rejected shreds changed state, records=%d", n)
	}
}

func TestPaginationExactAndOversize(t *testing.T) {
	s, _ := New(exampleSchema(), 3, 100)
	s.Shred(map[string]any{"id": int64(0)})
	s.Shred(map[string]any{"id": int64(1)})
	s.Shred(map[string]any{"id": int64(2), "author": map[string]any{
		"ids": []any{int64(5), int64(6)},
	}})
	s.Shred(map[string]any{"id": int64(3)})

	pages := s.Pages("author.ids")
	want := []PageInfo{
		{StartRecord: 0, RecordCount: 3, EntryCount: 4},
		{StartRecord: 3, RecordCount: 1, EntryCount: 1},
	}
	if !reflect.DeepEqual(pages, want) {
		t.Fatalf("pages = %+v", pages)
	}

	s2, _ := New([]Field{{Name: "x", Rep: Required}}, 1, 100)
	s2.Shred(map[string]any{"x": int64(1)})
	s2.Shred(map[string]any{"x": int64(2)})
	if ps := s2.Pages("x"); len(ps) != 2 || ps[0].EntryCount != 1 || ps[1].StartRecord != 1 {
		t.Fatalf("exact pages: %+v", ps)
	}

	s3, _ := New(exampleSchema(), 2, 100)
	s3.Shred(map[string]any{"id": int64(0), "author": map[string]any{
		"ids": []any{int64(1), int64(2), int64(3)},
	}})
	if ps := s3.Pages("author.ids"); len(ps) != 1 || ps[0].EntryCount != 3 {
		t.Fatalf("oversize record page: %+v", ps)
	}
}

func TestMaxEntriesBoundary(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 2)
	if err := s.Shred(map[string]any{"id": int64(1), "author": map[string]any{
		"ids": []any{int64(5), int64(6)},
	}}); err != nil {
		t.Fatalf("equal maxEntries: %v", err)
	}
	err := s.Shred(map[string]any{"id": int64(2), "author": map[string]any{
		"ids": []any{int64(5), int64(6), int64(7)},
	}})
	if !errors.Is(err, ErrTooLarge) || ErrorPath(err) != "author.ids" {
		t.Fatalf("too large: %v", err)
	}
	if s.RecordCount() != 1 {
		t.Fatalf("rejected record changed state")
	}
}

func TestStats(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 100)
	s.Shred(map[string]any{"id": int64(10), "author": map[string]any{
		"name": int64(5), "ids": []any{int64(1), int64(9)},
	}})
	s.Shred(map[string]any{"id": int64(-3)})
	st := s.Stats("id")
	if st != (Stats{Nulls: 0, Present: 2, Min: -3, Max: 10}) {
		t.Fatalf("id stats %+v", st)
	}
	st = s.Stats("author.ids")
	if st != (Stats{Nulls: 1, Present: 2, Min: 1, Max: 9}) {
		t.Fatalf("ids stats %+v", st)
	}
	if got := s.Stats("nope"); got != (Stats{}) {
		t.Fatalf("unknown col stats %+v", got)
	}
}

func TestEntriesReadAndEmptyAssemble(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 100)
	if got := s.Assemble(); len(got) != 0 {
		t.Fatalf("empty assemble: %#v", got)
	}
	s.Shred(map[string]any{"id": int64(1)})
	s.Assemble()
	// 5 leaf columns, one entry per record.
	if got := s.EntriesRead(); got != 5 {
		t.Fatalf("entriesRead = %d", got)
	}
	s.Assemble()
	if got := s.EntriesRead(); got != 10 {
		t.Fatalf("entriesRead cumulative = %d", got)
	}
}
