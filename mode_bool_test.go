package coercion

import "testing"

func categoriesStrict(input any, target Target) []ErrorCategory {
	_, err := New(Strict).Convert(input, target)
	if err == nil {
		return []ErrorCategory{}
	}
	var errs Errors
	if asErrors(err, &errs) {
		return errs.Categories()
	}
	category, _ := CategoryOf(err)
	return []ErrorCategory{category}
}

func asErrors(err error, target *Errors) bool {
	switch typed := err.(type) {
	case Errors:
		*target = typed
		return true
	default:
		return false
	}
}

func TestStrictErrorsCorrespondToLenientDegradations(t *testing.T) {
	target := Target{Kind: Int64Slice}
	input := []any{"9223372036854775808", 2.75, "bad", int64(1)}

	strictCategories := categoriesStrict(input, target)
	loose, err := New(Lenient).Convert(input, target)
	if err != nil {
		t.Fatalf("lenient failed: %v", err)
	}
	looseCategories := loose.Categories()
	if len(strictCategories) != len(looseCategories) {
		t.Fatalf("strict %v != lenient %v", strictCategories, looseCategories)
	}
	for i := range strictCategories {
		if strictCategories[i] != looseCategories[i] {
			t.Fatalf("position %d: strict %v, lenient %v", i, strictCategories, looseCategories)
		}
	}
}

func TestBoolAcceptanceSetIsClosed(t *testing.T) {
	accepted := []any{true, false, int64(1), int64(0), float64(1), float64(0),
		"true", "FALSE", " 1 ", "\tTrue\n"}
	for _, value := range accepted {
		if _, err := New(Strict).Convert(value, Target{Kind: BoolKind}); err != nil {
			t.Fatalf("accepted value %#v: %v", value, err)
		}
	}

	rejected := []any{"yes", "no", "2", int64(2), float64(2), " true x", ""}
	for _, value := range rejected {
		_, err := New(Strict).Convert(value, Target{Kind: BoolKind})
		category, ok := CategoryOf(err)
		if !ok || category != Invalid {
			t.Fatalf("rejected value %#v produced %q: %v", value, category, err)
		}
	}
}

func TestBoolWhitespaceRule(t *testing.T) {
	result, err := New(Strict).Convert("\t true \n", Target{Kind: BoolKind})
	if err != nil || result.Value != true {
		t.Fatalf("trimmed true = %#v, %v", result, err)
	}
	if _, err := New(Strict).Convert("tr ue", Target{Kind: BoolKind}); err == nil {
		t.Fatal("internal whitespace was accepted")
	}
}

func TestLenientDoesNotSwallowInvalidScalar(t *testing.T) {
	_, err := New(Lenient).Convert("yes", Target{Kind: BoolKind})
	category, ok := CategoryOf(err)
	if !ok || category != Invalid {
		t.Fatalf("category = %q, err = %v", category, err)
	}
}
