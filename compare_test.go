package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestInt64Float64Comparable(t *testing.T) {
	ev := NewEvaluator(64)
	props := map[string]any{"i": int64(1), "f": 1.5}

	cases := []struct {
		tree Predicate
		want Tri
	}{
		{&Compare{Property: "i", Op: Eq, Literal: 1.0}, True},
		{&Compare{Property: "f", Op: Eq, Literal: int64(1)}, False},
		{&Compare{Property: "i", Op: Lt, Literal: 1.5}, True},
		{&Compare{Property: "i", Op: Gt, Literal: 0.9}, True},
	}
	for idx, tc := range cases {
		got, err := ev.Eval(props, tc.tree)
		if err != nil || got.Value != tc.want {
			t.Fatalf("case %d = (%s,%v), want %s", idx, got.Value, err, tc.want)
		}
	}
}

func TestIncomparableTypesAreDecidableError(t *testing.T) {
	ev := NewEvaluator(64)
	_, err := ev.Eval(
		map[string]any{"name": "abc"},
		&Compare{Property: "name", Op: Eq, Literal: int64(1)},
	)
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("want *TypeError, got %v", err)
	}
	if te.Property != "name" || te.Op != Eq {
		t.Fatalf("error localization = {%q %s}, want name/eq", te.Property, te.Op)
	}
	if te.LeftType != "string" || te.RightType != "int64" {
		t.Fatalf("types = %q vs %q, want string vs int64", te.LeftType, te.RightType)
	}
}

func TestBoolLtIsTypeError(t *testing.T) {
	ev := NewEvaluator(64)
	for _, op := range []Op{Lt, Gt} {
		_, err := ev.Eval(
			map[string]any{"flag": true},
			&Compare{Property: "flag", Op: op, Literal: false},
		)
		var te *TypeError
		if !errors.As(err, &te) || te.Op != op || te.Property != "flag" {
			t.Fatalf("bool %s: want localized TypeError, got %v", op, err)
		}
	}

	got, err := ev.Eval(map[string]any{"flag": true},
		&Compare{Property: "flag", Op: Eq, Literal: true})
	if err != nil || got.Value != True {
		t.Fatalf("bool eq = (%s,%v), want true", got.Value, err)
	}
}

func TestNaNIsUnknownNotError(t *testing.T) {
	ev := NewEvaluator(64)

	cases := []struct {
		props map[string]any
		lit   any
	}{
		{map[string]any{"x": math.NaN()}, int64(1)},
		{map[string]any{"x": 1.0}, math.NaN()},
	}
	for idx, tc := range cases {
		for _, op := range []Op{Eq, Lt, Gt} {
			got, err := ev.Eval(tc.props, &Compare{Property: "x", Op: op, Literal: tc.lit})
			if err != nil {
				t.Fatalf("case %d %s: got error %v, want unknown", idx, op, err)
			}
			if got.Value != Unknown {
				t.Fatalf("case %d %s = %s, want unknown", idx, op, got.Value)
			}
		}
}
