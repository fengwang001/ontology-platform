package ontology

import "testing"

// constAttrs is a fixed attribute map used to build leaves with a known
// truth value: tLeaf is True, fLeaf is False, uLeaf is Unknown.
var constAttrs = map[string]any{"x": int64(1)}

func tLeaf() Pred { return EqTo("x", int64(1)) }       // True under constAttrs
func fLeaf() Pred { return EqTo("x", int64(2)) }       // False under constAttrs
func uLeaf() Pred { return EqTo("missing", int64(0)) } // Unknown: attr absent

func leafFor(v Tri) Pred {
	switch v {
	case True:
		return tLeaf()
	case False:
		return fLeaf()
	default:
		return uLeaf()
	}
}

func evalOK(t *testing.T, e *Evaluator, p Pred, attrs map[string]any) Tri {
	t.Helper()
	v, err := e.Eval(p, attrs)
	if err != nil {
		t.Fatalf("Eval(%v) returned unexpected error: %v", p, err)
	}
	return v
}

func TestKleeneAndTruthTable(t *testing.T) {
	e := NewEvaluator(8)
	want := [3][3]Tri{
		// L= False,        True,         Unknown  (rows: L, cols: R)
		{False, False, False},
		{False, True, Unknown},
		{False, Unknown, Unknown},
	}
	vals := [3]Tri{False, True, Unknown}
	for i, l := range vals {
		for j, r := range vals {
			got := evalOK(t, e, AndP(leafFor(l), leafFor(r)), constAttrs)
			if got != want[i][j] {
				t.Errorf("And(%s, %s) = %s, want %s", l, r, got, want[i][j])
			}
			if g := And(l, r); g != want[i][j] {
				t.Errorf("algebra And(%s, %s) = %s, want %s", l, r, g, want[i][j])
			}
		}
	}
}

func TestKleeneOrTruthTable(t *testing.T) {
	e := NewEvaluator(8)
	want := [3][3]Tri{
		{False, True, Unknown},
		{True, True, True},
		{Unknown, True, Unknown},
	}
	vals := [3]Tri{False, True, Unknown}
	for i, l := range vals {
		for j, r := range vals {
			got := evalOK(t, e, OrP(leafFor(l), leafFor(r)), constAttrs)
			if got != want[i][j] {
				t.Errorf("Or(%s, %s) = %s, want %s", l, r, got, want[i][j])
			}
			if g := Or(l, r); g != want[i][j] {
				t.Errorf("algebra Or(%s, %s) = %s, want %s", l, r, g, want[i][j])
			}
		}
	}
}

func TestKleeneNotTruthTable(t *testing.T) {
	e := NewEvaluator(8)
	want := map[Tri]Tri{False: True, True: False, Unknown: Unknown}
	for in, w := range want {
		got := evalOK(t, e, NotP(leafFor(in)), constAttrs)
		if got != w {
			t.Errorf("Not(%s) = %s, want %s", in, got, w)
		}
		if g := Not(in); g != w {
			t.Errorf("algebra Not(%s) = %s, want %s", in, g, w)
		}
	}
}

func TestMissingAttributeIsUnknownNotFalse(t *testing.T) {
	e := NewEvaluator(8)
	for _, p := range []Pred{
		EqTo("nope", int64(1)),
		LtTo("nope", 1.5),
		GtTo("nope", "s"),
	} {
		if got := evalOK(t, e, p, map[string]any{}); got != Unknown {
			t.Errorf("missing attr: got %s, want Unknown", got)
		}
	}
	// Unknown must not collapse to False inside And/Or either.
	if got := evalOK(t, e, AndP(tLeaf(), uLeaf()), constAttrs); got != Unknown {
		t.Errorf("And(True, Unknown) = %s, want Unknown", got)
	}
}

func TestIsNullResolvesUnknown(t *testing.T) {
	e := NewEvaluator(8)
	if got := evalOK(t, e, IsNull("nope"), map[string]any{}); got != True {
		t.Errorf("IsNull(missing) = %s, want True", got)
	}
	if got := evalOK(t, e, IsNull("x"), constAttrs); got != False {
		t.Errorf("IsNull(present) = %s, want False", got)
	}
	// IsNull never yields Unknown, even for a missing attribute.
	for _, attrs := range []map[string]any{{}, constAttrs, nil} {
		got := evalOK(t, e, IsNull("x"), attrs)
		if got == Unknown {
			t.Errorf("IsNull returned Unknown for attrs %v", attrs)
		}
	}
}
