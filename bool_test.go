package ontology

import "testing"

func TestBoolAcceptanceSetClosed(t *testing.T) {
	c := NewConverter(Strict)

	accepted := map[any]bool{
		true: true, false: false,
		int64(1): true, int64(0): false,
		1: true, 0: false,
		1.0: true, 0.0: false,
		"true": true, "TRUE": true, "True": true,
		"false": false, "FALSE": false,
		"1": true, "0": false,
	}
	for in, want := range accepted {
		out, err := c.CoerceValue(in, Target{Kind: Bool})
		if err != nil {
			t.Fatalf("%v must be accepted, got %v", in, err)
		}
		if out.Bool != want {
			t.Fatalf("%v: want %v", in, want)
		}
	}

	rejected := []any{2, -1, 2.0, 0.5, "yes", "no", "y", "t", " true", "false ", "", nil}
	for _, in := range rejected {
		tgt := Target{Kind: Bool}
		_, err := c.CoerceValue(in, tgt)
		if err == nil {
			t.Fatalf("%v must be rejected", in)
		}
		ce := AsCoerceError(err)
		if ce.Category != CatInvalidBool && ce.Category != CatTypeMismatch && ce.Category != CatNull {
			t.Fatalf("%v: want invalid_bool/type_mismatch, got %s", in, ce.Category)
		}
	}
}

func TestBoolWhitespaceIsSignificant(t *testing.T) {
	c := NewConverter(Strict)
	// Leading/trailing whitespace is never trimmed: these must fail.
	for _, s := range []string{" true", "true ", " false", "\ttrue", "true\n"} {
		if _, err := c.CoerceValue(s, Target{Kind: Bool}); err == nil {
			t.Fatalf("whitespace-padded %q must fail", s)
		}
	}
}

func TestBoolYesAndTwoFail(t *testing.T) {
	c := NewConverter(Strict)
	_, errYes := c.CoerceValue("yes", Target{Kind: Bool})
	if ce := AsCoerceError(errYes); ce == nil || ce.Category != CatInvalidBool {
		t.Fatalf("yes must be invalid_bool, got %v", errYes)
	}
	_, errTwo := c.CoerceValue(2, Target{Kind: Bool})
	if ce := AsCoerceError(errTwo); ce == nil || ce.Category != CatInvalidBool {
		t.Fatalf("2 must be invalid_bool, got %v", errTwo)
	}
}
