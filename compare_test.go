package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestInt64Float64CrossCompare(t *testing.T) {
	attrs := map[string]any{"n": int64(1)}
	res, err := NewEvaluator(4).Evaluate(Comparison{Op: Eq, Attr: "n", Value: float64(1.0)}, attrs)
	if err != nil || res.Value != True {
		t.Fatalf("int64(1) Eq float64(1.0) = %v, %v; want True, nil", res.Value, err)
	}
	res, err = NewEvaluator(4).Evaluate(Comparison{Op: Lt, Attr: "n", Value: float64(1.5)}, attrs)
	if err != nil || res.Value != True {
		t.Fatalf("int64(1) Lt float64(1.5) = %v, %v; want True, nil", res.Value, err)
	}
}

func TestBoolLtIsTypeError(t *testing.T) {
	attrs := map[string]any{"flag": true}
	_, err := NewEvaluator(4).Evaluate(Comparison{Op: Lt, Attr: "flag", Value: false}, attrs)
	var terr *TypeError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TypeError", err)
	}
	if terr.Attr != "flag" || terr.AttrType != "bool" || terr.ValueType != "bool" {
		t.Fatalf("TypeError detail = %+v", terr)
	}
	// bool Eq is fine.
	res, err := NewEvaluator(4).Evaluate(Comparison{Op: Eq, Attr: "flag", Value: true}, attrs)
	if err != nil || res.Value != True {
		t.Fatalf("bool Eq = %v, %v; want True, nil", res.Value, err)
	}
}

func TestMismatchedTypesAreTypeError(t *testing.T) {
	attrs := map[string]any{"name": "abc"}
	_, err := NewEvaluator(4).Evaluate(Comparison{Op: Eq, Attr: "name", Value: int64(1)}, attrs)
	var terr *TypeError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TypeError", err)
	}
	if terr.Attr != "name" || terr.AttrType != "string" || terr.ValueType != "int64" {
		t.Fatalf("TypeError detail = %+v", terr)
	}
}

func TestNaNIsUnknownNotError(t *testing.T) {
	attrs := map[string]any{"x": math.NaN()}
	for _, op := range []Op{Eq, Lt, Gt} {
		res, err := NewEvaluator(4).Evaluate(Comparison{Op: op, Attr: "x", Value: 1.0}, attrs)
		if err != nil {
			t.Fatalf("NaN %s literal: unexpected error %v", op, err)
		}
		if res.Value != Unknown {
			t.Fatalf("NaN %s literal = %v, want Unknown", op, res.Value)
		}
	}
	res, err := NewEvaluator(4).Evaluate(
		Comparison{Op: Eq, Attr: "y", Value: math.NaN()},
		map[string]any{"y": 2.0})
	if err != nil || res.Value != Unknown {
		t.Fatalf("literal NaN Eq = %v, %v; want Unknown, nil", res.Value, err)
	}
}

func TestErrorInEvaluatedSubtreePropagates(t *testing.T) {
	bad := Comparison{Op: Eq, Attr: "name", Value: int64(1)}
	attrs := map[string]any{"a": int64(1), "name": "abc"}
	// And(True, type-error): the right side is evaluated, error must surface.
	_, err := NewEvaluator(8).Evaluate(AndPred{Left: trueLeaf, Right: bad}, attrs)
	var terr *TypeError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TypeError", err)
	}
	// And(Unknown, type-error): Unknown does not short-circuit, error must surface.
	_, err = NewEvaluator(8).Evaluate(AndPred{Left: unknownLeaf, Right: bad}, attrs)
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want *TypeError", err)
	}
}

func TestErrorInShortCircuitedSubtreeIsNotReported(t *testing.T) {
	bad := Comparison{Op: Eq, Attr: "name", Value: int64(1)}
	attrs := map[string]any{"a": int64(1), "name": "abc"}
	// And(False, type-error): right side never evaluated, no error, 1 leaf.
	res, err := NewEvaluator(8).Evaluate(AndPred{Left: falseLeaf, Right: bad}, attrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != False || res.Leaves != 1 {
		t.Fatalf("got (%v, %d leaves), want (False, 1 leaf)", res.Value, res.Leaves)
	}
	// Or(True, type-error): same for Or.
	res, err = NewEvaluator(8).Evaluate(OrPred{Left: trueLeaf, Right: bad}, attrs)
	if err != nil || res.Value != True || res.Leaves != 1 {
		t.Fatalf("got (%v, %d leaves, %v), want (True, 1 leaf, nil)", res.Value, res.Leaves, err)
	}
}
