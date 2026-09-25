package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestInt64Float64CrossCompare(t *testing.T) {
	ev := NewEvaluator(4)
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
		want  Trilean
	}{
		{"int attr eq float lit", Eq("a", float64(1.0)), map[string]any{"a": int64(1)}, True},
		{"float attr eq int lit", Eq("a", int64(1)), map[string]any{"a": float64(1.0)}, True},
		{"int attr lt float lit", Lt("a", float64(1.5)), map[string]any{"a": int64(1)}, True},
		{"float attr gt int lit", Gt("a", int64(2)), map[string]any{"a": float64(2.5)}, True},
		{"int attr neq float lit", Eq("a", float64(1.5)), map[string]any{"a": int64(1)}, False},
	}
	for _, c := range cases {
		v, err, _ := evalCase(t, ev, c.p, c.props)
		if err != nil || v != c.want {
			t.Errorf("%s: got (%s, %v), want (%s, nil)", c.name, v, err, c.want)
		}
	}
}

func TestStringCompare(t *testing.T) {
	ev := NewEvaluator(4)
	props := map[string]any{"s": "bob"}
	for _, c := range []struct {
		p    *Predicate
		want Trilean
	}{
		{Eq("s", "bob"), True},
		{Eq("s", "alice"), False},
		{Lt("s", "carol"), True},
		{Gt("s", "alice"), True},
	} {
		v, err, _ := evalCase(t, ev, c.p, props)
		if err != nil || v != c.want {
			t.Errorf("%v: got (%s, %v), want (%s, nil)", c.p, v, err, c.want)
		}
	}
}

func TestIncomparableTypesAreTypeError(t *testing.T) {
	ev := NewEvaluator(4)
	p := Eq("name", int64(42))
	v, err, _ := evalCase(t, ev, p, map[string]any{"name": "alice"})
	if err == nil || v != False {
		t.Fatalf("got (%s, %v), want (False, TypeError)", v, err)
	}
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a *TypeError", err)
	}
	if te.Attr != "name" || te.AttrType != "string" || te.ValueType != "int64" {
		t.Fatalf("TypeError fields wrong: %+v", te)
	}
	if !IsTypeError(err) {
		t.Fatal("IsTypeError returned false")
	}
}

func TestBoolEqOnly(t *testing.T) {
	ev := NewEvaluator(4)
	props := map[string]any{"flag": true}
	v, err, _ := evalCase(t, ev, Eq("flag", true), props)
	if err != nil || v != True {
		t.Fatalf("bool Eq: got (%s, %v), want (True, nil)", v, err)
	}
	for _, p := range []*Predicate{Lt("flag", true), Gt("flag", false)} {
		v, err, _ := evalCase(t, ev, p, props)
		if v != False || !IsTypeError(err) {
			t.Fatalf("bool %s: got (%s, %v), want (False, TypeError)", p.Op, v, err)
		}
		var te *TypeError
		if errors.As(err, &te) && (te.Attr != "flag" || te.AttrType != "bool") {
			t.Fatalf("TypeError fields wrong: %+v", te)
		}
	}
}

func TestNaNYieldsUnknownNotError(t *testing.T) {
	ev := NewEvaluator(4)
	nan := math.NaN()
	cases := []struct {
		name  string
		p     *Predicate
		props map[string]any
	}{
		{"nan attr eq", Eq("a", float64(1.0)), map[string]any{"a": nan}},
		{"nan attr lt", Lt("a", float64(1.0)), map[string]any{"a": nan}},
		{"nan attr gt", Gt("a", float64(1.0)), map[string]any{"a": nan}},
		{"nan lit eq", Eq("a", nan), map[string]any{"a": float64(1.0)}},
		{"nan vs nan", Eq("a", nan), map[string]any{"a": nan}},
	}
	for _, c := range cases {
		v, err, n := evalCase(t, ev, c.p, c.props)
		if err != nil || v != Unknown || n != 1 {
			t.Errorf("%s: got (%s, %v, %d), want (Unknown, nil, 1)", c.name, v, err, n)
		}
	}
}

func TestUnsupportedAttrTypeIsTypeError(t *testing.T) {
	ev := NewEvaluator(4)
	v, err, _ := evalCase(t, ev, Eq("a", int64(1)), map[string]any{"a": []int{1}})
	if v != False || !IsTypeError(err) {
		t.Fatalf("got (%s, %v), want (False, TypeError)", v, err)
	}
}
