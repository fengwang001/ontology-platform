package ontology

import (
	"math"
	"testing"
)

// Characterization tests: every assertion in this file pins the
// CURRENT, actual behavior of the evaluator, not the desired one.
// All cases are expected to pass against the unmodified
// implementation. See FINDINGS.md for where actual behavior diverges
// from documented intent.

// TestLargeInt64Precision pins the float64 conversion behavior of
// compareNumeric for int64 values beyond 2^53: distinct int64 values
// collapse to the same float64 and compare as equal.
func TestLargeInt64Precision(t *testing.T) {
	ev := NewEvaluator(4)
	const (
		bigA = int64(9007199254740993) // 2^53 + 1, not representable in float64
		bigB = int64(9007199254740992) // 2^53, exactly representable
	)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		// Two distinct int64 values are judged equal after float64
		// conversion: float64(bigA) rounds to float64(bigB).
		{"int64 attr eq distinct int64 lit", Eq("id", bigB), map[string]any{"id": bigA}, True},
		{"int64 attr lt distinct int64 lit", Lt("id", bigB), map[string]any{"id": bigA}, False},
		{"int64 attr gt distinct int64 lit", Gt("id", bigB), map[string]any{"id": bigA}, False},
		// int64 attr vs float64 literal: same collapse.
		{"int64 attr eq float64 lit", Eq("id", float64(bigB)), map[string]any{"id": bigA}, True},
		// float64 attr vs int64 literal: same collapse.
		{"float64 attr eq int64 lit", Eq("id", bigA), map[string]any{"id": float64(bigB)}, True},
		// Values below 2^53 compare exactly (control case).
		{"small int64 eq exact", Eq("id", int64(42)), map[string]any{"id": int64(42)}, True},
		{"small int64 neq exact", Eq("id", int64(43)), map[string]any{"id": int64(42)}, False},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}

// TestNilAttributeValue pins the behavior of an attribute that is
// present in the property set but holds a nil value: IsNull reports
// it as present (False) while leaf comparisons return a *TypeError.
func TestNilAttributeValue(t *testing.T) {
	ev := NewEvaluator(4)
	props := map[string]any{"x": nil}

	// IsNull only checks key presence, so a present-but-nil
	// attribute is reported as "not null".
	v, err, _ := evalCase(t, ev, IsNull("x"), props)
	if err != nil || v != False {
		t.Errorf("IsNull on nil value: got (%s, %v), want (False, nil)", v, err)
	}

	// Leaf comparisons on the same attribute hit the default branch
	// of compareLeaf and yield a *TypeError with AttrType "<nil>".
	for _, p := range []*Predicate{Eq("x", int64(1)), Lt("x", int64(1)), Gt("x", 1.5)} {
		v, err, _ := evalCase(t, ev, p, props)
		if v != False || !IsTypeError(err) {
			t.Errorf("%v on nil value: got (%s, %v), want (False, TypeError)", p.Op, v, err)
			continue
		}
		te := err.(*TypeError)
		if te.AttrType != "<nil>" || te.Reason != "unsupported attribute type" {
			t.Errorf("%v on nil value: TypeError = %+v, want AttrType <nil>, Reason %q",
				p.Op, te, "unsupported attribute type")
		}
	}

	// A truly absent attribute yields Unknown for both IsNull
	// (inverted) and leaf comparisons, with no error.
	v, err, _ = evalCase(t, ev, IsNull("missing"), props)
	if err != nil || v != True {
		t.Errorf("IsNull on absent attr: got (%s, %v), want (True, nil)", v, err)
	}
	v, err, _ = evalCase(t, ev, Eq("missing", int64(1)), props)
	if err != nil || v != Unknown {
		t.Errorf("Eq on absent attr: got (%s, %v), want (Unknown, nil)", v, err)
	}
}

// TestEmptyAndOr pins the identity elements of empty And/Or nodes:
// AndP() with no children yields True, OrP() yields False.
func TestEmptyAndOr(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name string
		p    *Predicate
		want Trilean
	}{
		{"empty And is True", AndP(), True},
		{"empty Or is False", OrP(), False},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, nil)
		if err != nil || v != c.want || n != 0 {
			t.Errorf("%s: got (%s, %v, %d leaves), want (%s, nil, 0 leaves)", c.name, v, err, n, c.want)
		}
	}
}

// TestNotArityErrorIsUntyped pins that a Not node with 0 or 2
// children fails with a plain fmt.Errorf error that is recognized by
// neither IsTypeError nor IsDepthError.
func TestNotArityErrorIsUntyped(t *testing.T) {
	ev := NewEvaluator(8)
	cases := []struct {
		name string
		p    *Predicate
	}{
		{"Not with 0 children", &Predicate{Kind: KindNot}},
		{"Not with 2 children", &Predicate{Kind: KindNot, Children: []*Predicate{
			Eq("a", int64(1)), Eq("a", int64(2)),
		}}},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, map[string]any{"a": int64(1)})
		if err == nil || v != False {
			t.Errorf("%s: got (%s, %v), want (False, non-nil error)", c.name, v, err)
			continue
		}
		if IsTypeError(err) {
			t.Errorf("%s: error %v unexpectedly matches IsTypeError", c.name, err)
		}
		if IsDepthError(err) {
			t.Errorf("%s: error %v unexpectedly matches IsDepthError", c.name, err)
		}
	}
}

// TestSignedZeroEquality pins that -0.0 and +0.0 compare equal,
// because cmpOrdered uses plain float64 operators.
func TestSignedZeroEquality(t *testing.T) {
	ev := NewEvaluator(4)
	negZero := math.Copysign(0, -1)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		{"-0.0 attr eq +0.0 lit", Eq("z", 0.0), map[string]any{"z": negZero}, True},
		{"+0.0 attr eq -0.0 lit", Eq("z", negZero), map[string]any{"z": 0.0}, True},
		{"-0.0 attr lt +0.0 lit", Lt("z", 0.0), map[string]any{"z": negZero}, False},
		{"-0.0 attr gt +0.0 lit", Gt("z", 0.0), map[string]any{"z": negZero}, False},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}
