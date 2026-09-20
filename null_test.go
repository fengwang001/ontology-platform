package ontology

import (
	"reflect"
	"testing"
)

func TestMissingAndNilExcludedFromEqualityIndex(t *testing.T) {
	s := New("color")
	s.Upsert("missing", map[string]any{"other": 1})
	s.Upsert("nilval", map[string]any{"color": nil})
	s.Upsert("real", map[string]any{"color": "red"})
	for _, v := range []any{"red", "blue", "", 0, nil} {
		got := s.Query("color", v)
		for _, id := range got {
			if id == "missing" || id == "nilval" {
				t.Fatalf("Query(%v) returned %s, which must not be indexed", v, id)
			}
		}
	}
	if got := s.Query("color", "red"); !reflect.DeepEqual(got, []string{"real"}) {
		t.Fatalf("Query red = %v, want [real]", got)
	}
}

func TestIsNullDistinguishesMissingFromNil(t *testing.T) {
	s := New("color")
	s.Upsert("m1", map[string]any{})
	s.Upsert("m2", map[string]any{"other": 1})
	s.Upsert("n1", map[string]any{"color": nil})
	s.Upsert("v1", map[string]any{"color": "red"})
	missing, nilVal := s.IsNull("color")
	if !reflect.DeepEqual(missing, []string{"m1", "m2"}) {
		t.Fatalf("missing = %v, want [m1 m2]", missing)
	}
	if !reflect.DeepEqual(nilVal, []string{"n1"}) {
		t.Fatalf("nilVal = %v, want [n1]", nilVal)
	}
}

func TestEmptyStringIsNormalValue(t *testing.T) {
	s := New("color")
	s.Upsert("empty", map[string]any{"color": ""})
	s.Upsert("nilval", map[string]any{"color": nil})
	if got := s.Query("color", ""); !reflect.DeepEqual(got, []string{"empty"}) {
		t.Fatalf("empty string must be indexed, Query = %v", got)
	}
	missing, nilVal := s.IsNull("color")
	if len(missing) != 0 || !reflect.DeepEqual(nilVal, []string{"nilval"}) {
		t.Fatalf("empty string must not count as null: missing=%v nil=%v", missing, nilVal)
	}
}

func TestNullCountsSumToTotal(t *testing.T) {
	s := New("color")
	s.Upsert("a", map[string]any{"color": "red"})
	s.Upsert("b", map[string]any{"color": ""})
	s.Upsert("c", map[string]any{"color": nil})
	s.Upsert("d", map[string]any{"other": 1})
	indexed, missing, nilVal := s.AttrCounts("color")
	if indexed != 2 || missing != 1 || nilVal != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", indexed, missing, nilVal)
	}
	if indexed+missing+nilVal != s.Len() {
		t.Fatalf("counts sum %d != Len %d", indexed+missing+nilVal, s.Len())
	}
}

func TestUpdateToNilEntersIsNull(t *testing.T) {
	s := New("color")
	s.Upsert("x", map[string]any{"color": "red"})
	s.Upsert("x", map[string]any{"color": nil})
	if got := s.Query("color", "red"); len(got) != 0 {
		t.Fatalf("nil update must remove equality entry, got %v", got)
	}
	_, nilVal := s.IsNull("color")
	if !reflect.DeepEqual(nilVal, []string{"x"}) {
		t.Fatalf("nil-updated entity must appear in IsNull, got %v", nilVal)
	}
	// Removing the attribute entirely moves it to "missing".
	s.Upsert("x", map[string]any{"other": 1})
	missing, nilVal := s.IsNull("color")
	if !reflect.DeepEqual(missing, []string{"x"}) || len(nilVal) != 0 {
		t.Fatalf("attr removal must count as missing: missing=%v nil=%v", missing, nilVal)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}
