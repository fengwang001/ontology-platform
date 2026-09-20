package ontology

import (
	"reflect"
	"testing"
)

func nullFixture() *Store {
	s := NewStore("tag")
	s.Upsert("has-value", map[string]any{"tag": "x"})
	s.Upsert("has-empty", map[string]any{"tag": ""})
	s.Upsert("has-nil", map[string]any{"tag": nil})
	s.Upsert("has-none", map[string]any{"other": 1})
	return s
}

func TestNullSemantics(t *testing.T) {
	s := nullFixture()

	// Missing and nil never match any concrete equality lookup.
	for _, v := range []any{"x", "", 0, nil} {
		got := s.Lookup("tag", v)
		for _, id := range got {
			if id == "has-nil" || id == "has-none" {
				t.Fatalf("Lookup(%v) returned %q", v, id)
			}
		}
	}

	// Empty string is a normal, indexed value.
	if got := s.Lookup("tag", ""); !reflect.DeepEqual(got, []string{"has-empty"}) {
		t.Fatalf("empty string lookup = %v", got)
	}

	// IsNull distinguishes missing from nil.
	info := s.IsNull("tag")
	if !reflect.DeepEqual(info.Missing, []string{"has-none"}) {
		t.Fatalf("missing = %v", info.Missing)
	}
	if !reflect.DeepEqual(info.Nil, []string{"has-nil"}) {
		t.Fatalf("nil = %v", info.Nil)
	}
}

func TestCountsSumToRowCount(t *testing.T) {
	s := NewStore("color")
	fillColors(s, 1000)
	for i := 0; i < 50; i++ {
		s.Upsert(string(rune('A'+i))+"-nil", map[string]any{"color": nil})
		s.Upsert(string(rune('a'+i))+"-miss", map[string]any{"x": i})
	}
	indexed, missing, null := s.Counts("color")
	if indexed+missing+null != s.RowCount() {
		t.Fatalf("%d+%d+%d != %d", indexed, missing, null, s.RowCount())
	}
	if null != 50 || missing != 50 {
		t.Fatalf("null=%d missing=%d, want 50/50", null, missing)
	}
	info := s.IsNull("color")
	if info.Total() != missing+null {
		t.Fatalf("IsNull total %d != %d", info.Total(), missing+null)
	}
}

func TestCountsOnUnindexedAttr(t *testing.T) {
	s := nullFixture()
	indexed, missing, null := s.Counts("tag")
	if indexed != 2 || missing != 1 || null != 1 {
		t.Fatalf("counts = %d/%d/%d", indexed, missing, null)
	}
}
