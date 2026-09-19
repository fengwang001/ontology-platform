package ontology

import "testing"

// The three absent-ish cases must never collapse into one zero value:
// missing key vs present-nil vs present-zero give distinct statuses and
// distinct error categories.
func TestMissingNullZeroTrisection(t *testing.T) {
	strict := NewConverter(Strict)

	inputs := map[string]map[string]any{
		"missing": {},
		"null":    {"v": nil},
		"zero":    {"v": ""},
	}

	for _, nullable := range []bool{false, true} {
		tgt := Target{Kind: String, Nullable: nullable}

		// Missing.
		out, err := strict.Coerce(inputs["missing"], "v", tgt)
		if !out.IsMissing() || out.IsExplicitNull() || out.IsZeroValue(String) {
			t.Fatalf("missing: status not distinguished: %+v", out)
		}
		if !nullable {
			if ce := AsCoerceError(err); ce == nil || ce.Category != CatMissing {
				t.Fatalf("missing non-nullable: want CatMissing, got %v", err)
			}
		} else if err != nil {
			t.Fatalf("missing nullable must not error, got %v", err)
		}

		// Explicit null.
		out, err = strict.Coerce(inputs["null"], "v", tgt)
		if out.IsMissing() || !out.IsExplicitNull() || out.IsZeroValue(String) {
			t.Fatalf("null: status not distinguished: %+v", out)
		}
		if !nullable {
			if ce := AsCoerceError(err); ce == nil || ce.Category != CatNull {
				t.Fatalf("null non-nullable: want CatNull, got %v", err)
			}
		} else if err != nil {
			t.Fatalf("null nullable must not error, got %v", err)
		}

		// Zero value.
		out, err = strict.Coerce(inputs["zero"], "v", tgt)
		if out.IsMissing() || out.IsExplicitNull() || !out.IsZeroValue(String) {
			t.Fatalf("zero: status not distinguished: %+v", out)
		}
		if err != nil {
			t.Fatalf("zero value must never error, got %v", err)
		}
		if len(out.Records) != 0 {
			t.Fatalf("zero value must not produce records, got %+v", out.Records)
		}
	}
}

func TestZeroValuesPerType(t *testing.T) {
	c := NewConverter(Strict)
	cases := []struct {
		t Target
		v any
	}{
		{Target{Kind: Int64, Nullable: true}, int64(0)},
		{Target{Kind: Float64, Nullable: true}, 0.0},
		{Target{Kind: Bool, Nullable: true}, false},
		{Target{Kind: String, Nullable: true}, ""},
	}
	for _, tc := range cases {
		out, err := c.Coerce(map[string]any{"v": tc.v}, "v", tc.t)
		if err != nil || !out.IsZeroValue(tc.t.Kind) || out.Status != StatusPresent {
			t.Fatalf("%v: zero value mishandled: %+v err=%v", tc.t.Kind, out, err)
		}
	}
}

func TestNullableDistinctOutcomes(t *testing.T) {
	c := NewConverter(Strict)
	tgt := Target{Kind: Int64, Nullable: true}

	missing, err := c.Coerce(map[string]any{}, "v", tgt)
	if err != nil || !missing.IsMissing() || missing.IsExplicitNull() {
		t.Fatalf("nullable missing: %+v %v", missing, err)
	}
	null, err := c.Coerce(map[string]any{"v": nil}, "v", tgt)
	if err != nil || null.IsMissing() || !null.IsExplicitNull() {
		t.Fatalf("nullable null: %+v %v", null, err)
	}
}
