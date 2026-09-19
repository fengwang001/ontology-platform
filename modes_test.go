package ontology

import (
	"math"
	"reflect"
	"testing"
)

func categorySet(cats []Category) map[Category]bool {
	m := map[Category]bool{}
	for _, c := range cats {
		m[c] = true
	}
	return m
}

// For every distorting input, strict-mode error categories and
// lenient-mode record categories must be identical; lenient mode must
// not swallow any class of problem.
func TestStrictLenientCategoryParity(t *testing.T) {
	cases := []struct {
		name string
		t    Target
		in   any
		want Category
	}{
		{"string overflow", Target{Kind: Int64}, "9223372036854775808", CatOverflow},
		{"float fraction", Target{Kind: Int64}, 3.75, CatFractionLost},
		{"float precision", Target{Kind: Int64}, float64(1 << 53), CatPrecisionLost},
		{"float overflow", Target{Kind: Int64}, 1e20, CatOverflow},
		{"int precision", Target{Kind: Float64}, int64(1<<53 + 1), CatPrecisionLost},
		{"bad bool string", Target{Kind: Bool}, "yes", CatInvalidBool},
		{"bad bool number", Target{Kind: Bool}, 2, CatInvalidBool},
		{"malformed int", Target{Kind: Int64}, "abc", CatMalformedNumber},
		{"type mismatch", Target{Kind: Int64}, true, CatTypeMismatch},
	}

	strict := NewConverter(Strict)
	lenient := NewConverter(Lenient)
	for _, tc := range cases {
		_, sErr := strict.CoerceValue(tc.in, tc.t)
		lOut, lErr := lenient.CoerceValue(tc.in, tc.t)
		if sErr == nil {
			t.Fatalf("%s: expected a strict error", tc.name)
		}
		if lErr != nil {
			t.Fatalf("%s: lenient must not error, got %v", tc.name, lErr)
		}
		gotStrict := AsCoerceError(sErr).Category
		var gotLenient Category
		if len(lOut.Records) != 1 {
			t.Fatalf("%s: want exactly 1 record, got %+v", tc.name, lOut.Records)
		}
		gotLenient = lOut.Records[0].Category
		if gotStrict != tc.want || gotLenient != tc.want {
			t.Fatalf("%s: strict=%s lenient=%s want=%s",
				tc.name, gotStrict, gotLenient, tc.want)
		}
	}
}

func TestLenientProducesUsableValues(t *testing.T) {
	c := NewConverter(Lenient)

	out, err := c.CoerceValue("9223372036854775808", Target{Kind: Int64})
	if err != nil || out.Int64 != math.MaxInt64 {
		t.Fatalf("overflow lenient: %+v %v", out, err)
	}
	if r := out.Records[0]; r.Detail == "" || r.From != "9223372036854775808" || r.To != int64(math.MaxInt64) {
		t.Fatalf("overflow record missing raw/before-after: %+v", r)
	}

	out, err = c.CoerceValue(3.75, Target{Kind: Int64})
	if err != nil || out.Int64 != 3 {
		t.Fatalf("fraction lenient: %+v %v", out, err)
	}
	if r := out.Records[0]; r.Category != CatFractionLost ||
		r.Detail == "" || r.To != int64(3) || r.From != 3.75 {
		t.Fatalf("fraction record bad: %+v", r)
	}

	out, err = c.CoerceValue("yes", Target{Kind: Bool})
	if err != nil || out.Bool != false {
		t.Fatalf("invalid bool lenient: %+v %v", out, err)
	}
	if out.Records[0].Category != CatInvalidBool {
		t.Fatalf("bad bool record: %+v", out.Records)
	}

	// Clean conversion has no records and no errors.
	out, err = c.CoerceValue("42", Target{Kind: Int64})
	if err != nil || out.Int64 != 42 || len(out.Records) != 0 {
		t.Fatalf("clean conversion: %+v %v", out, err)
	}
}

func TestCleanInputsMatchAcrossModes(t *testing.T) {
	inputs := []any{"abc", int64(7), 3.5, true, "false"}
	targets := []Target{
		{Kind: String}, {Kind: Int64}, {Kind: Float64}, {Kind: Bool}, {Kind: Bool},
	}
	s, l := NewConverter(Strict), NewConverter(Lenient)
	for i, in := range inputs {
		so, se := s.CoerceValue(in, targets[i])
		lo, le := l.CoerceValue(in, targets[i])
		if se != nil || le != nil || !reflect.DeepEqual(so, lo) {
			t.Fatalf("clean input %d: mismatch %+v/%v vs %+v/%v", i, so, se, lo, le)
		}
	}
}
