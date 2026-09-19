package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestInt64Float64CrossTypeEquality(t *testing.T) {
	e := NewEvaluator(8)
	cases := []struct {
		attrVal any
		lit     any
		op      Op
		want    Tri
	}{
		{int64(1), 1.0, Eq, True},
		{1.0, int64(1), Eq, True},
		{int64(2), 1.5, Gt, True},
		{1.5, int64(2), Lt, True},
		{int64(1), 1.5, Lt, True},
		{int64(3), 3.0, Lt, False},
		// Exact mixed comparison near the float64 precision boundary:
		// 2^53+1 is representable as int64 but not as float64.
		{int64(1<<53 + 1), float64(1 << 53), Eq, False},
		{int64(1 << 53), float64(1 << 53), Eq, True},
		{int64(1<<53 + 1), float64(1 << 53), Gt, True},
	}
	for _, c := range cases {
		p := &Cmp{Op: c.op, Attr: "a", Value: c.lit}
		got := evalOK(t, e, p, map[string]any{"a": c.attrVal})
		if got != c.want {
			t.Errorf("%v(%T) %s %v(%T) = %s, want %s",
				c.attrVal, c.attrVal, c.op, c.lit, c.lit, got, c.want)
		}
	}
}

func TestStringComparisons(t *testing.T) {
	e := NewEvaluator(8)
	attrs := map[string]any{"s": "b"}
	if got := evalOK(t, e, LtTo("s", "c"), attrs); got != True {
		t.Errorf(`"b" < "c" = %s, want True`, got)
	}
	if got := evalOK(t, e, GtTo("s", "a"), attrs); got != True {
		t.Errorf(`"b" > "a" = %s, want True`, got)
	}
	if got := evalOK(t, e, EqTo("s", "b"), attrs); got != True {
		t.Errorf(`"b" == "b" = %s, want True`, got)
	}
}

func TestIncomparableTypesYieldTypeError(t *testing.T) {
	e := NewEvaluator(8)
	cases := []struct {
		attrVal any
		lit     any
	}{
		{"s", int64(1)},      // string vs int64
		{int64(1), "s"},      // int64 vs string
		{true, 1.0},          // bool vs float64
		{int64(1), true},     // int64 vs bool
		{[]int{1}, int64(1)}, // unsupported attr type
	}
	for _, c := range cases {
		p := EqTo("a", c.lit)
		_, err := e.Eval(p, map[string]any{"a": c.attrVal})
		var te *TypeError
		if !errors.As(err, &te) {
			t.Fatalf("attr %v(%T) vs lit %v(%T): err = %v, want *TypeError",
				c.attrVal, c.attrVal, c.lit, c.lit, err)
		}
		if te.Attr != "a" || te.AttrType == "" || te.LitType == "" {
			t.Errorf("TypeError not decidable/locatable: %+v", te)
		}
	}
}

func TestBoolOnlySupportsEq(t *testing.T) {
	e := NewEvaluator(8)
	attrs := map[string]any{"b": true}
	if got := evalOK(t, e, EqTo("b", true), attrs); got != True {
		t.Errorf("bool Eq = %s, want True", got)
	}
	for _, op := range []Op{Lt, Gt} {
		p := &Cmp{Op: op, Attr: "b", Value: false}
		_, err := e.Eval(p, attrs)
		var te *TypeError
		if !errors.As(err, &te) {
			t.Errorf("bool %s: err = %v, want *TypeError", op, err)
		}
	}
}

func TestNaNComparisonIsUnknownNotError(t *testing.T) {
	e := NewEvaluator(8)
	nan := math.NaN()
	for _, op := range []Op{Eq, Lt, Gt} {
		// NaN on the attribute side.
		p := &Cmp{Op: op, Attr: "a", Value: 1.0}
		got, err := e.Eval(p, map[string]any{"a": nan})
		if err != nil || got != Unknown {
			t.Errorf("NaN(attr) %s 1.0 = (%s, %v), want (Unknown, nil)", op, got, err)
		}
		// NaN on the literal side, including Eq.
		p = &Cmp{Op: op, Attr: "a", Value: nan}
		got, err = e.Eval(p, map[string]any{"a": 1.0})
		if err != nil || got != Unknown {
			t.Errorf("1.0 %s NaN(lit) = (%s, %v), want (Unknown, nil)", op, got, err)
		}
	}
	// NaN vs NaN with Eq is still Unknown, never True.
	got, err := e.Eval(EqTo("a", nan), map[string]any{"a": nan})
	if err != nil || got != Unknown {
		t.Errorf("NaN == NaN = (%s, %v), want (Unknown, nil)", got, err)
	}
}
