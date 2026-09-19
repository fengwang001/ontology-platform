package coercion

import (
	"errors"
	"testing"
)

func TestMissingNullAndZeroAreDistinguishable(t *testing.T) {
	properties := map[string]any{
		"null": nil,
		"zero": int64(0),
	}
	target := Target{Kind: Int64Kind}
	converter := New(Strict)

	missing, err := converter.FromMap(properties, "missing", target)
	if !missing.IsMissing() || missing.IsNull() || missing.IsPresent() {
		t.Fatalf("missing state = %v", missing.State)
	}
	if category, ok := CategoryOf(err); !ok || category != Missing {
		t.Fatalf("missing category = %q, %v", category, ok)
	}

	null, err := converter.FromMap(properties, "null", target)
	if !null.IsNull() || null.IsMissing() || null.IsPresent() {
		t.Fatalf("null state = %v", null.State)
	}
	if category, ok := CategoryOf(err); !ok || category != Null {
		t.Fatalf("null category = %q, %v", category, ok)
	}

	zero, err := converter.FromMap(properties, "zero", target)
	if !zero.IsPresent() || err != nil || zero.Value != int64(0) {
		t.Fatalf("zero = %#v, err = %v", zero, err)
	}
}

func TestNullableMissingAndNullProduceDistinctStates(t *testing.T) {
	properties := map[string]any{"null": nil}
	target := Target{Kind: String, Nullable: true}
	converter := New(Lenient)

	missing, err := converter.FromMap(properties, "missing", target)
	if err != nil || !missing.IsMissing() || missing.IsNull() {
		t.Fatalf("nullable missing = %#v, err = %v", missing, err)
	}

	null, err := converter.FromMap(properties, "null", target)
	if err != nil || !null.IsNull() || null.IsMissing() || null.Value != nil {
		t.Fatalf("nullable null = %#v, err = %v", null, err)
	}
}

func TestScalarZeroValuesArePresent(t *testing.T) {
	converter := New(Strict)
	cases := []struct {
		name   string
		target Kind
		value  any
		want   any
	}{
		{"empty string", String, "", ""},
		{"zero int64", Int64Kind, int64(0), int64(0)},
		{"zero float", Float64Kind, float64(0), float64(0)},
		{"false bool", BoolKind, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := converter.Convert(tc.value, Target{Kind: tc.target})
			if err != nil || !result.IsPresent() || result.Value != tc.want {
				t.Fatalf("Convert() = %#v, %v", result, err)
			}
		})
	}
}

func TestErrorsRetainStructuredCategories(t *testing.T) {
	_, err := New(Strict).FromMap(nil, "x", Target{Kind: BoolKind})
	var errs Errors
	if !errors.As(err, &errs) || len(errs) != 1 || errs[0].Category != Missing {
		t.Fatalf("err = %#v", err)
	}
	if errs.Categories()[0] != Missing {
		t.Fatalf("categories = %v", errs.Categories())
	}
}
