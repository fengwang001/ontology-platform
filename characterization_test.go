package ontology

import (
	"math"
	"testing"
)

// This file pins down the *current* (characterized) behaviour of the
// evaluator on edge cases the existing tests leave open. Every
// assertion describes what the implementation actually does today,
// not necessarily what the docs promise. See FINDINGS.md for the gaps.

// TestCharLeafCountCountsOnlyCompare pins down that only KindCompare
// nodes increment Result.leaves: KindIsNull is a leaf node but is not
// counted as a "leaf comparison", alone, negated, or short-circuited.
func TestCharLeafCountCountsOnlyCompare(t *testing.T) {
	ev := NewEvaluator(16)
	props := map[string]any{"a": int64(1), "b": int64(1)}
	cases := []struct {
		name string
		p    *Predicate
		want Trilean
		n    int
	}{
		{"isnull alone", IsNull("missing"), True, 0},
		{"not isnull", NotP(IsNull("missing")), False, 0},
		{"isnull then compare", AndP(IsNull("missing"), Eq("a", int64(1))), True, 1},
		{"compare then isnull", AndP(Eq("a", int64(1)), IsNull("missing")), True, 1},
		{"or isnull then compare", OrP(IsNull("a"), Eq("b", int64(1))), True, 1},
		{"or compare short-circuits isnull", OrP(Eq("a", int64(1)), IsNull("missing")), True, 1},
		{"and compare false skips isnull", AndP(Eq("a", int64(2)), IsNull("missing")), False, 1},
		{"two isnulls", AndP(IsNull("missing"), IsNull("a")), False, 0},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, props)
		if err != nil || v != c.want || n != c.n {
			t.Errorf("%s: got (%s, %v, leaves=%d), want (%s, nil, leaves=%d)",
				c.name, v, err, n, c.want, c.n)
		}
	}
}

// TestCharLargeInt64Float64Precision pins down that int64 operands are
// converted via float64 before comparison. Past 2^53 distinct int64
// values round to the same float64: distinct integers compare equal,
// both to each other and to an intermediate float64 literal.
func TestCharLargeInt64Float64Precision(t *testing.T) {
	ev := NewEvaluator(4)
	const base = int64(1) << 53
	neighbor := float64(base) + 1.0 // rounds back to float64(2^53)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		{"int64 2^53 eq int64 2^53+1", Eq("a", base+1), map[string]any{"a": base}, True},
		{"int64 2^53 eq float64(2^53+1)", Eq("a", neighbor), map[string]any{"a": base}, True},
		{"float64(2^53+1) attr eq int64 2^53 lit", Eq("a", base), map[string]any{"a": neighbor}, True},
		{"int64 2^53 lt int64 2^53+1", Lt("a", base+1), map[string]any{"a": base}, False},
		{"int64 max eq int64 max-1", Eq("a", int64(math.MaxInt64-1)),
			map[string]any{"a": int64(math.MaxInt64)}, True},
		// Control: below the precision boundary integers stay distinct.
		{"small int64 1 eq int64 2", Eq("a", int64(2)), map[string]any{"a": int64(1)}, False},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want || n != 1 {
			t.Errorf("%s: got (%s, %v, leaves=%d), want (%s, nil, 1)",
				c.name, v, err, n, c.want)
		}
	}
}

// TestCharEmptyAndOrIdentity pins down that zero-child And/Or nodes are
// accepted and act as algebraic identities: And() is True, Or() is
// False, with zero leaves evaluated and no error.
func TestCharEmptyAndOrIdentity(t *testing.T) {
	ev := NewEvaluator(8)
	props := map[string]any{"a": int64(1)}
	cases := []struct {
		name     string
		p        *Predicate
		want     Trilean
		noLeaves bool
	}{
		{"empty and", AndP(), True, true},
		{"empty or", OrP(), False, true},
		{"not empty and", NotP(AndP()), False, true},
		{"not empty or", NotP(OrP()), True, true},
		{"empty and as or child", OrP(AndP()), True, true},
		{"empty or as and child", AndP(OrP()), False, true},
		{"empty and then false compare", AndP(AndP(), Eq("a", int64(2))), False, false},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
		if c.noLeaves && n != 0 {
			t.Errorf("%s: leaves=%d, want 0", c.name, n)
		}
	}
}

// TestCharIsNullKeyPresence pins down that IsNull tests map key
// presence, not nil-ness: a key mapped to nil is present (IsNull ->
// False); that same nil fed to a Compare hits the default branch and
// yields a *TypeError.
func TestCharIsNullKeyPresence(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name      string
		p         *Predicate
		props     map[string]any
		want      Trilean
		typeError bool
		n         int
	}{
		{"missing key", IsNull("x"), map[string]any{}, True, false, 0},
		{"present value", IsNull("x"), map[string]any{"x": int64(1)}, False, false, 0},
		{"present nil value", IsNull("x"), map[string]any{"x": nil}, False, false, 0},
		{"not missing key", NotP(IsNull("x")), map[string]any{}, False, false, 0},
		{"not present nil", NotP(IsNull("x")), map[string]any{"x": nil}, True, false, 0},
		{"compare nil value", Eq("x", int64(1)), map[string]any{"x": nil}, False, true, 1},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, c.props)
		if c.typeError {
			if !IsTypeError(err) || v != False {
				t.Errorf("%s: got (%s, %v), want (False, TypeError)", c.name, v, err)
			}
		} else if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
		if n != c.n {
			t.Errorf("%s: leaves=%d, want %d", c.name, n, c.n)
		}
	}
}

// TestCharNotArityErrorClassification pins down that a Not node whose
// child count is not exactly one returns a plain fmt-created error,
// classified neither as *TypeError nor *DepthError, although Eval's
// doc only promises "decidable" errors.
func TestCharNotArityErrorClassification(t *testing.T) {
	ev := NewEvaluator(8)
	props := map[string]any{"a": int64(1), "b": int64(1)}
	for _, c := range []struct {
		name     string
		children []*Predicate
	}{
		{"zero children", nil},
		{"two children", []*Predicate{Eq("a", int64(1)), Eq("b", int64(1))}},
	} {
		p := &Predicate{Kind: KindNot, Children: c.children}
		v, err, n := evalCase(t, ev, p, props)
		if err == nil {
			t.Fatalf("%s: expected error, got %s", c.name, v)
		}
		if IsTypeError(err) {
			t.Errorf("%s: plain arity error classified as TypeError: %v", c.name, err)
		}
		if IsDepthError(err) {
			t.Errorf("%s: plain arity error classified as DepthError: %v", c.name, err)
		}
		if v != False || n != 0 {
			t.Errorf("%s: got (%s, leaves=%d), want (False, 0)", c.name, v, n)
		}
	}
	// Control: the one-child form still works.
	if v, err, n := evalCase(t, ev, NotP(Eq("a", int64(1))), props); err != nil || v != False || n != 1 {
		t.Fatalf("control NotP: got (%s, %v, leaves=%d), want (False, nil, 1)", v, err, n)
	}
}

// TestCharDepthBoundaryLeafCount pins down the ordering "depth check on
// node entry, before leaf counting": a Compare leaf exactly at
// maxDepth is allowed and counted; a node one level deeper fails with
// *DepthError and adds no leaves. IsNull never counts at any depth.
func TestCharDepthBoundaryLeafCount(t *testing.T) {
	props := map[string]any{"a": int64(1)}

	// A bare root leaf sits at depth 1 and is allowed at every depth.
	for _, d := range []int{1, 2, 3, 8} {
		ev := NewEvaluator(d)
		if v, err, n := evalCase(t, ev, Eq("a", int64(1)), props); err != nil || v != True || n != 1 {
			t.Errorf("compare at maxDepth=%d: got (%s, %v, leaves=%d), want (True, nil, 1)",
				d, v, err, n)
		}
		if v, err, n := evalCase(t, ev, IsNull("a"), props); err != nil || v != False || n != 0 {
			t.Errorf("isnull at maxDepth=%d: got (%s, %v, leaves=%d), want (False, nil, 0)",
				d, v, err, n)
		}
	}

	// k Not wrappers put the Compare leaf at depth k+1; walk all shapes.
	for _, d := range []int{1, 2, 3, 4} {
		ev := NewEvaluator(d)
		for k := 0; k <= d+1; k++ {
			p := Eq("a", int64(1))
			for i := 0; i < k; i++ {
				p = NotP(p)
			}
			v, err, n := evalCase(t, ev, p, props)
			wantVal := True
			if k%2 == 1 {
				wantVal = False // each Not flips the leaf's True
			}
			if k+1 <= d {
				if err != nil || v != wantVal || n != 1 {
					t.Errorf("maxDepth=%d wrappers=%d: got (%s, %v, leaves=%d), want (%s, nil, 1)",
						d, k, v, err, n, wantVal)
				}
			} else if !IsDepthError(err) || v != False || n != 0 {
				t.Errorf("maxDepth=%d wrappers=%d: got (%s, %v, leaves=%d), want (False, DepthError, 0)",
					d, k, v, err, n)
			}
		}
	}

	// Children of And/Or live at depth 2; a rejected leaf counts zero,
	// while a sibling evaluated before it still counts.
	cases := []struct {
		name      string
		p         *Predicate
		want      Trilean
		n         int
		depthFail bool
	}{
		{"and child leaf at depth 2 allowed", AndP(Eq("a", int64(1))), True, 1, false},
		{"or child leaf at depth 2 allowed", OrP(Eq("a", int64(1))), True, 1, false},
		{"and child wrapped past depth", AndP(NotP(Eq("a", int64(1)))), False, 0, true},
		{"counted first leaf then depth failure",
			AndP(Eq("a", int64(1)), NotP(Eq("a", int64(1)))), False, 1, true},
		{"false sibling short-circuits before deep subtree",
			AndP(Eq("a", int64(2)), NotP(Eq("a", int64(1)))), False, 1, false},
	}
	for _, c := range cases {
		ev := NewEvaluator(2)
		v, err, n := evalCase(t, ev, c.p, props)
		if c.depthFail && !IsDepthError(err) {
			t.Errorf("%s: err=%v, want DepthError", c.name, err)
		}
		if !c.depthFail && err != nil {
			t.Errorf("%s: unexpected err=%v", c.name, err)
		}
		if v != c.want || n != c.n {
			t.Errorf("%s: got (%s, leaves=%d), want (%s, leaves=%d)",
				c.name, v, n, c.want, c.n)
		}
	}
}
