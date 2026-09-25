package ontology

// Characterization tests: every assertion in this file pins the
// CURRENT, actual behavior of the evaluator, including behaviors
// that are known or suspected defects (see FINDINGS.md). These tests
// must pass against the unmodified implementation; if an assertion
// here starts failing, the implementation's observable behavior has
// changed and the change must be reviewed deliberately.

import (
	"math"
	"testing"
)

// 2^53 and its neighbours: 2^53+1 is not representable as float64
// and rounds down to 2^53 when converted.
const (
	two53      = int64(9007199254740992) // 2^53, exactly representable
	two53Plus1 = int64(9007199254740993) // 2^53+1, rounds to 2^53 in float64
)

// TestCharInt64PrecisionLoss pins the precision loss caused by
// compareNumeric converting int64 operands to float64: distinct
// int64 values above 2^53 collapse onto the same float64 and are
// therefore judged equal / not ordered.
func TestCharInt64PrecisionLoss(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		// Below 2^53 int64 comparison is exact.
		{"below 2^53 lt", Lt("a", two53), map[string]any{"a": two53 - 1}, True},
		{"below 2^53 eq", Eq("a", two53-1), map[string]any{"a": two53 - 1}, True},
		// 2^53+1 rounds to 2^53: two different int64s compare equal.
		{"2^53+1 eq 2^53 (int lit)", Eq("a", two53), map[string]any{"a": two53Plus1}, True},
		{"2^53 eq 2^53+1 (int lit)", Eq("a", two53Plus1), map[string]any{"a": two53}, True},
		{"2^53 lt 2^53+1 is lost", Lt("a", two53Plus1), map[string]any{"a": two53}, False},
		{"2^53+1 gt 2^53 is lost", Gt("a", two53), map[string]any{"a": two53Plus1}, False},
		// Same loss against a float64 literal.
		{"2^53+1 eq float64(2^53)", Eq("a", float64(two53)), map[string]any{"a": two53Plus1}, True},
		// math.MaxInt64 and math.MaxInt64-1 both round to 2^63.
		{"maxint64 eq maxint64-1", Eq("a", int64(math.MaxInt64-1)), map[string]any{"a": int64(math.MaxInt64)}, True},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}

// TestCharNilProperty pins the contradictory handling of a property
// that is present but nil: IsNull reports False ("not null") while
// every leaf comparison reports a *TypeError ("unsupported type").
func TestCharNilProperty(t *testing.T) {
	ev := NewEvaluator(4)
	props := map[string]any{"a": nil}

	v, err, _ := evalCase(t, ev, IsNull("a"), props)
	if err != nil || v != False {
		t.Errorf("IsNull on present-nil attr: got (%s, %v), want (False, nil)", v, err)
	}

	cases := []struct {
		name string
		p    *Predicate
	}{
		{"Eq nil literal", Eq("a", nil)},
		{"Eq int64 literal", Eq("a", int64(1))},
		{"Eq string literal", Eq("a", "x")},
		{"Lt int64 literal", Lt("a", int64(1))},
		{"Gt int64 literal", Gt("a", int64(1))},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, props)
		if v != False || !IsTypeError(err) {
			t.Errorf("%s on present-nil attr: got (%s, %v), want (False, *TypeError)", c.name, v, err)
		}
	}

	// Contrast: a genuinely absent attribute yields Unknown for
	// comparisons and True for IsNull, with no error.
	absent := map[string]any{}
	v, err, _ = evalCase(t, ev, IsNull("a"), absent)
	if err != nil || v != True {
		t.Errorf("IsNull on absent attr: got (%s, %v), want (True, nil)", v, err)
	}
	v, err, _ = evalCase(t, ev, Eq("a", int64(1)), absent)
	if err != nil || v != Unknown {
		t.Errorf("Eq on absent attr: got (%s, %v), want (Unknown, nil)", v, err)
	}
}

// TestCharEmptyAndOr pins the vacuous results of And/Or nodes with
// zero children: And() is True, Or() is False, no error, no leaves.
func TestCharEmptyAndOr(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name string
		p    *Predicate
		want Trilean
	}{
		{"empty AndP", AndP(), True},
		{"empty OrP", OrP(), False},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, map[string]any{})
		if err != nil || v != c.want || n != 0 {
			t.Errorf("%s: got (%s, %v, %d leaves), want (%s, nil, 0 leaves)", c.name, v, err, n, c.want)
		}
	}
}

// TestCharNotArityErrorIsUntyped pins that a Not node with 0 or 2
// children fails with a plain fmt.Errorf error that is recognized by
// neither IsTypeError nor IsDepthError.
func TestCharNotArityErrorIsUntyped(t *testing.T) {
	ev := NewEvaluator(4)
	leaf := Eq("a", int64(1))
	cases := []struct {
		name string
		p    *Predicate
	}{
		{"Not with 0 children", &Predicate{Kind: KindNot}},
		{"Not with 2 children", &Predicate{Kind: KindNot, Children: []*Predicate{leaf, leaf}}},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, map[string]any{"a": int64(1)})
		if err == nil {
			t.Errorf("%s: got nil error, want non-nil", c.name)
			continue
		}
		if v != False {
			t.Errorf("%s: got value %s, want False", c.name, v)
		}
		if IsTypeError(err) || IsDepthError(err) {
			t.Errorf("%s: error %v unexpectedly typed (IsTypeError=%v, IsDepthError=%v)",
				c.name, err, IsTypeError(err), IsDepthError(err))
		}
	}
}

// TestCharNegativeZero pins that -0.0 and +0.0 are judged equal by
// the numeric comparator (IEEE 754 float comparison semantics).
func TestCharNegativeZero(t *testing.T) {
	ev := NewEvaluator(4)
	negZero := math.Copysign(0, -1)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		{"-0.0 attr eq +0.0 lit", Eq("a", float64(0)), map[string]any{"a": negZero}, True},
		{"+0.0 attr eq -0.0 lit", Eq("a", negZero), map[string]any{"a": float64(0)}, True},
		{"-0.0 attr lt +0.0 lit", Lt("a", float64(0)), map[string]any{"a": negZero}, False},
		{"-0.0 attr gt +0.0 lit", Gt("a", float64(0)), map[string]any{"a": negZero}, False},
		{"-0.0 attr eq int64 0 lit", Eq("a", int64(0)), map[string]any{"a": negZero}, True},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}
