package ontology

import (
	"math"
	"testing"
)

// TestInt64PrecisionLossAbove2Pow53 pins the current behavior of
// compareNumeric for int64 values beyond the float64 mantissa: both
// operands are converted to float64, so distinct int64 values can
// compare equal.
func TestInt64PrecisionLossAbove2Pow53(t *testing.T) {
	ev := NewEvaluator(4)
	const (
		p53   = int64(1) << 53 // 9007199254740992, exactly representable
		p53p1 = p53 + 1        // rounds to p53 as float64
		p53p2 = p53 + 2        // exactly representable
		p53p3 = p53 + 3        // rounds to p53+4 as float64
	)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		// 2^53+1 and 2^53 are distinct int64s but collapse to the same
		// float64, so Eq holds and both orderings are False.
		{"2^53+1 eq 2^53 int lit", Eq("a", p53), map[string]any{"a": p53p1}, True},
		{"2^53+1 eq 2^53 float lit", Eq("a", float64(p53)), map[string]any{"a": p53p1}, True},
		{"2^53+1 lt 2^53", Lt("a", p53), map[string]any{"a": p53p1}, False},
		{"2^53+1 gt 2^53", Gt("a", p53), map[string]any{"a": p53p1}, False},
		// 2^53+3 rounds up to 2^53+4, so it does not collapse with 2^53+2.
		{"2^53+3 eq 2^53+2", Eq("a", p53p2), map[string]any{"a": p53p3}, False},
		{"2^53+3 gt 2^53+2", Gt("a", p53p2), map[string]any{"a": p53p3}, True},
		// Below the boundary comparison is still exact.
		{"2^53-1 eq 2^53-2", Eq("a", p53-2), map[string]any{"a": p53 - 1}, False},
		{"2^53-1 gt 2^53-2", Gt("a", p53-2), map[string]any{"a": p53 - 1}, True},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
	// Identity holds at every split point around the boundary: equal
	// int64 operands round to the same float64.
	for _, split := range []int64{p53 - 1, p53, p53p1, p53p2} {
		p := Eq("a", split)
		v, err, _ := evalCase(t, ev, p, map[string]any{"a": split})
		if err != nil || v != True {
			t.Errorf("identity at %d: got (%s, %v), want (True, nil)", split, v, err)
		}
	}
}

// TestNilAttrSemantics pins the contradictory handling of an attribute
// that is present with a nil value: IsNull reports it as not null,
// while leaf comparisons report a *TypeError.
func TestNilAttrSemantics(t *testing.T) {
	ev := NewEvaluator(4)
	props := map[string]any{"a": nil}

	v, err, _ := evalCase(t, ev, IsNull("a"), props)
	if err != nil || v != False {
		t.Errorf("IsNull(present nil): got (%s, %v), want (False, nil)", v, err)
	}

	for _, p := range []*Predicate{Eq("a", int64(1)), Lt("a", int64(1)), Gt("a", int64(1)), Eq("a", nil)} {
		v, err, _ := evalCase(t, ev, p, props)
		if v != False || !IsTypeError(err) {
			t.Errorf("%v on nil attr: got (%s, %v), want (False, TypeError)", p.Op, v, err)
		}
		if te, ok := err.(*TypeError); ok && te.AttrType != "<nil>" {
			t.Errorf("%v on nil attr: AttrType = %q, want %q", p.Op, te.AttrType, "<nil>")
		}
	}

	// Contrast with a genuinely missing attribute: IsNull is True and
	// comparisons yield Unknown without error.
	v, err, _ = evalCase(t, ev, IsNull("missing"), props)
	if err != nil || v != True {
		t.Errorf("IsNull(missing): got (%s, %v), want (True, nil)", v, err)
	}
	v, err, _ = evalCase(t, ev, Eq("missing", int64(1)), props)
	if err != nil || v != Unknown {
		t.Errorf("Eq(missing): got (%s, %v), want (Unknown, nil)", v, err)
	}
}

// TestEmptyAndOr pins the behavior of And/Or nodes with no children:
// they evaluate to the identity element (True for And, False for Or)
// instead of reporting an arity error.
func TestEmptyAndOr(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name string
		p    *Predicate
		want Trilean
	}{
		{"empty And", AndP(), True},
		{"empty Or", OrP(), False},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, nil)
		if err != nil || v != c.want || n != 0 {
			t.Errorf("%s: got (%s, %v, %d), want (%s, nil, 0)", c.name, v, err, n, c.want)
		}
	}
}

// TestNotArityErrorIsUntyped pins that a Not node with 0 or 2 children
// fails with a plain fmt.Errorf error that is neither a *TypeError nor
// a *DepthError.
func TestNotArityErrorIsUntyped(t *testing.T) {
	ev := NewEvaluator(8)
	leaf := Eq("a", int64(1))
	cases := []struct {
		name string
		p    *Predicate
	}{
		{"0 children", &Predicate{Kind: KindNot}},
		{"2 children", &Predicate{Kind: KindNot, Children: []*Predicate{leaf, leaf}}},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, map[string]any{"a": int64(1)})
		if err == nil || v != False {
			t.Errorf("%s: got (%s, %v), want (False, error)", c.name, v, err)
			continue
		}
		if IsTypeError(err) || IsDepthError(err) {
			t.Errorf("%s: error %v unexpectedly matches IsTypeError/IsDepthError", c.name, err)
		}
	}
}

// TestNegativeZeroEqualsPositiveZero pins that -0.0 and +0.0 compare
// equal under Eq and are unordered under Lt/Gt.
func TestNegativeZeroEqualsPositiveZero(t *testing.T) {
	ev := NewEvaluator(4)
	neg := math.Copysign(0, -1)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		{"-0.0 eq +0.0", Eq("a", 0.0), map[string]any{"a": neg}, True},
		{"-0.0 lt +0.0", Lt("a", 0.0), map[string]any{"a": neg}, False},
		{"-0.0 gt +0.0", Gt("a", 0.0), map[string]any{"a": neg}, False},
		{"-0.0 eq int 0", Eq("a", int64(0)), map[string]any{"a": neg}, True},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}
