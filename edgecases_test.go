package ontology

import "testing"

// Characterization tests pinning the *current* behaviour of the
// evaluator on edge cases the doc comments leave unspecified. Every
// assertion records what the implementation actually does today, not
// what the comments promise.

// TestCharacterizeLeafCountOnlyCountsCompare pins down that only
// KindCompare leaves increment Result.LeafCount; KindIsNull leaves do
// not, regardless of where they appear.
func TestCharacterizeLeafCountOnlyCountsCompare(t *testing.T) {
	ev := NewEvaluator(16)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		wantV Trilean
		wantN int
	}{
		{"single IsNull counts zero", IsNull("missing"), map[string]any{}, True, 0},
		{"IsNull then Compare counts one", AndP(IsNull("missing"), Eq("a", int64(1))), map[string]any{"a": int64(1)}, True, 1},
		{"Compare then IsNull counts one", AndP(Eq("a", int64(1)), IsNull("missing")), map[string]any{"a": int64(1)}, True, 1},
		{"IsNull sandwiched between Compares counts two", AndP(Eq("a", int64(1)), IsNull("missing"), Eq("b", int64(1))), map[string]any{"a": int64(1), "b": int64(1)}, True, 2},
		{"Not(IsNull) counts zero", NotP(IsNull("missing")), map[string]any{}, False, 0},
		{"Or of three IsNull counts zero", OrP(IsNull("m1"), IsNull("m2"), IsNull("m3")), map[string]any{}, True, 0},
		// And(False, IsNull, Compare): evaluation stops at the first
		// False; neither later node is touched.
		{"short circuit skips IsNull and Compare", AndP(Eq("a", int64(2)), IsNull("missing"), Eq("b", int64(1))), map[string]any{"a": int64(1), "b": int64(1)}, False, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err, n := evalCase(t, ev, tc.p, tc.props)
			if err != nil || v != tc.wantV || n != tc.wantN {
				t.Fatalf("got (%s, %v, %d), want (%s, nil, %d)", v, err, n, tc.wantV, tc.wantN)
			}
		})
	}
}

// TestCharacterizeLargeInt64VsFloat64 pins the float64 conversion
// path: int64 operands are converted via float64(), so integers above
// 2^53 collapse onto their nearest float64 and compare equal to values
// distinct in int64 arithmetic.
func TestCharacterizeLargeInt64VsFloat64(t *testing.T) {
	ev := NewEvaluator(4)
	// 1<<53 is exactly representable as float64; 1<<53+1 rounds back.
	base := int64(1) << 53
	neighbor := base + 1
	baseF := float64(base)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		wantV Trilean
	}{
		{"large int64 equals its own float64", Eq("a", baseF), map[string]any{"a": base}, True},
		{"adjacent int64 (2^53+1) equals float64(2^53)", Eq("a", baseF), map[string]any{"a": neighbor}, True},
		{"two distinct large int64 operands compare equal", Eq("a", neighbor), map[string]any{"a": base}, True},
		{"Lt swallowed by rounding", Lt("a", baseF), map[string]any{"a": neighbor}, False},
		{"Gt swallowed by rounding", Gt("a", baseF), map[string]any{"a": neighbor}, False},
		{"float64 attr equal to adjacent large int64 literal", Eq("a", neighbor), map[string]any{"a": baseF}, True},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err, n := evalCase(t, ev, tc.p, tc.props)
			if err != nil || v != tc.wantV || n != 1 {
				t.Fatalf("got (%s, %v, %d), want (%s, nil, 1)", v, err, n, tc.wantV)
			}
		})
	}
}

// TestCharacterizeEmptyAndOr pins the identity-element behaviour for
// zero-child nodes, although the Predicate doc says "at least one".
func TestCharacterizeEmptyAndOr(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name  string
		p     *Predicate
		wantV Trilean
		wantN int
	}{
		{"empty And is True", AndP(), True, 0},
		{"empty Or is False", OrP(), False, 0},
		{"Not(empty And) is False", NotP(AndP()), False, 0},
		{"Not(empty Or) is True", NotP(OrP()), True, 0},
		{"And(empty And, leaf True)", AndP(AndP(), Eq("a", int64(1))), True, 1},
		{"Or(empty Or, leaf True)", OrP(OrP(), Eq("a", int64(1))), True, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err, n := evalCase(t, ev, tc.p, map[string]any{"a": int64(1)})
			if err != nil || v != tc.wantV || n != tc.wantN {
				t.Fatalf("got (%s, %v, %d), want (%s, nil, %d)", v, err, n, tc.wantV, tc.wantN)
			}
		})
	}
}

// TestCharacterizeIsNullKeyExistence pins that IsNull tests map-key
// presence, not whether the value is nil: a key mapped to nil is
// "present" and IsNull answers False. It also pins that a Compare on
// such a nil-valued attribute lands in the TypeError branch.
func TestCharacterizeIsNullKeyExistence(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name        string
		p           *Predicate
		props       map[string]any
		wantV       Trilean
		wantTypeErr bool
		wantN       int
	}{
		{"key missing: IsNull True", IsNull("x"), map[string]any{}, True, false, 0},
		{"key present with nil: IsNull False", IsNull("x"), map[string]any{"x": nil}, False, false, 0},
		{"key present with zero int: IsNull False", IsNull("x"), map[string]any{"x": int64(0)}, False, false, 0},
		{"nil value Compare is TypeError not Unknown", Eq("x", int64(1)), map[string]any{"x": nil}, False, true, 1},
		{"Not(IsNull) on nil value is True", NotP(IsNull("x")), map[string]any{"x": nil}, True, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err, n := evalCase(t, ev, tc.p, tc.props)
			if tc.wantTypeErr {
				if !IsTypeError(err) {
					t.Fatalf("got err %v, want *TypeError", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != tc.wantV || n != tc.wantN {
				t.Fatalf("got (%s, %d), want (%s, %d)", v, n, tc.wantV, tc.wantN)
			}
		})
	}
}

// TestCharacterizeNotArityError pins that a KindNot node whose child
// count is not exactly 1 yields a plain fmt error that neither
// IsTypeError nor IsDepthError recognizes, and that the arity check
// fires before children are evaluated.
func TestCharacterizeNotArityError(t *testing.T) {
	ev := NewEvaluator(8)
	cases := []struct {
		name string
		p    *Predicate
	}{
		{"Not with zero children", &Predicate{Kind: KindNot}},
		{"Not with two children", &Predicate{Kind: KindNot, Children: []*Predicate{Eq("a", int64(1)), Eq("b", int64(1))}}},
	}
	props := map[string]any{"a": int64(1), "b": int64(1)}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err, n := evalCase(t, ev, tc.p, props)
			if err == nil {
				t.Fatalf("want arity error, got nil with value %s", v)
			}
			if IsTypeError(err) {
				t.Fatalf("arity error must not classify as TypeError: %v", err)
			}
			if IsDepthError(err) {
				t.Fatalf("arity error must not classify as DepthError: %v", err)
			}
			if v != False || n != 0 {
				t.Fatalf("got (%s, %d), want (False, 0); children must stay unevaluated", v, n)
			}
		})
	}
}

// TestCharacterizeDepthBoundaryVsLeafCount pins the ordering of the
// depth check relative to leaf counting: the check runs on node entry
// before r.leaves++, so a leaf exactly at maxDepth is allowed and
// counted, while one level deeper is rejected with DepthError and not
// counted.
func TestCharacterizeDepthBoundaryVsLeafCount(t *testing.T) {
	cases := []struct {
		name        string
		maxDepth    int
		p           *Predicate
		props       map[string]any
		wantV       Trilean
		wantDepthEr bool
		wantN       int
	}{
		{"leaf at depth 1 with maxDepth 1 counted", 1, Eq("a", int64(1)), map[string]any{"a": int64(1)}, True, false, 1},
		{"leaf at depth 2 with maxDepth 1 rejected and not counted", 1, NotP(Eq("a", int64(1))), map[string]any{"a": int64(1)}, False, true, 0},
		// Not(False)=True so the And does not short-circuit and the
		// depth-3 leaf plus its depth-2 sibling are both counted.
		{"leaf at depth 3 with maxDepth 3 counted", 3, AndP(NotP(Eq("a", int64(1))), Eq("b", int64(1))), map[string]any{"a": int64(2), "b": int64(1)}, True, false, 2},
		{"leaf at depth 3 with maxDepth 2 rejected; earlier leaf counted", 2, AndP(Eq("a", int64(2)), NotP(Eq("b", int64(1)))), map[string]any{"a": int64(2), "b": int64(1)}, False, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := NewEvaluator(tc.maxDepth)
			v, err, n := evalCase(t, ev, tc.p, tc.props)
			if tc.wantDepthEr {
				if !IsDepthError(err) {
					t.Fatalf("got err %v, want *DepthError", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != tc.wantV || n != tc.wantN {
				t.Fatalf("got (%s, %d), want (%s, %d)", v, n, tc.wantV, tc.wantN)
			}
		})
	}
}
