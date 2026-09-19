package ontology

import (
	"errors"
	"testing"
)

func TestMissingAttributeIsUnknown(t *testing.T) {
	res, err := NewEvaluator(8).Evaluate(
		Comparison{Op: Gt, Attr: "nope", Value: int64(0)}, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != Unknown {
		t.Fatalf("missing attribute = %v, want Unknown", res.Value)
	}
	if res.Leaves != 1 {
		t.Fatalf("leaves = %d, want 1", res.Leaves)
	}
}

func TestIsNullResolvesUnknown(t *testing.T) {
	ev := NewEvaluator(8)
	res, err := ev.Evaluate(IsNullPred{Attr: "gone"}, map[string]any{"x": int64(1)})
	if err != nil || res.Value != True {
		t.Fatalf("IsNull(absent) = %v, %v; want True, nil", res.Value, err)
	}
	res, err = ev.Evaluate(IsNullPred{Attr: "x"}, map[string]any{"x": int64(1)})
	if err != nil || res.Value != False {
		t.Fatalf("IsNull(present) = %v, %v; want False, nil", res.Value, err)
	}
	// IsNull of a missing attribute inside Not becomes determinate.
	res, err = ev.Evaluate(NotPred{Child: IsNullPred{Attr: "gone"}}, map[string]any{})
	if err != nil || res.Value != False {
		t.Fatalf("Not(IsNull(absent)) = %v, %v; want False, nil", res.Value, err)
	}
}

func TestAndShortCircuitLeafCounts(t *testing.T) {
	cases := []struct {
		name        string
		left, right Predicate
		wantValue   Trilean
		wantLeaves  int
	}{
		{"false stops before right", falseLeaf, trueLeaf, False, 1},
		{"true evaluates both", trueLeaf, falseLeaf, False, 2},
		{"unknown does not short-circuit", unknownLeaf, falseLeaf, False, 2},
		{"unknown and true", unknownLeaf, trueLeaf, Unknown, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := evalTree(t, AndPred{Left: c.left, Right: c.right})
			if res.Value != c.wantValue || res.Leaves != c.wantLeaves {
				t.Fatalf("got (%v, %d leaves), want (%v, %d leaves)",
					res.Value, res.Leaves, c.wantValue, c.wantLeaves)
			}
		})
	}
}

func TestOrShortCircuitLeafCounts(t *testing.T) {
	cases := []struct {
		name        string
		left, right Predicate
		wantValue   Trilean
		wantLeaves  int
	}{
		{"true stops before right", trueLeaf, falseLeaf, True, 1},
		{"false evaluates both", falseLeaf, trueLeaf, True, 2},
		{"unknown does not short-circuit", unknownLeaf, trueLeaf, True, 2},
		{"unknown or false", unknownLeaf, falseLeaf, Unknown, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := evalTree(t, OrPred{Left: c.left, Right: c.right})
			if res.Value != c.wantValue || res.Leaves != c.wantLeaves {
				t.Fatalf("got (%v, %d leaves), want (%v, %d leaves)",
					res.Value, res.Leaves, c.wantValue, c.wantLeaves)
			}
		})
	}
}

func TestShortCircuitSkipsWholeSubtree(t *testing.T) {
	right := AndPred{Left: trueLeaf, Right: AndPred{Left: trueLeaf, Right: trueLeaf}}
	res := evalTree(t, AndPred{Left: falseLeaf, Right: right})
	if res.Value != False || res.Leaves != 1 {
		t.Fatalf("got (%v, %d leaves), want (False, 1 leaf)", res.Value, res.Leaves)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := Predicate(trueLeaf)
	for i := 0; i < 5; i++ {
		deep = NotPred{Child: deep}
	}
	_, err := NewEvaluator(3).Evaluate(deep, testAttrs)
	var depthErr *DepthError
	if !errors.As(err, &depthErr) {
		t.Fatalf("err = %v, want *DepthError", err)
	}
	if depthErr.Limit != 3 {
		t.Fatalf("limit = %d, want 3", depthErr.Limit)
	}
	if _, err := NewEvaluator(6).Evaluate(deep, testAttrs); err != nil {
		t.Fatalf("depth 6 tree within limit 6: unexpected %v", err)
	}
}

func TestEvaluationIsPureAndRepeatable(t *testing.T) {
	tree := AndPred{Left: trueLeaf, Right: OrPred{Left: unknownLeaf, Right: falseLeaf}}
	attrs := map[string]any{"a": int64(1)}
	ev := NewEvaluator(16)
	first, err := ev.Evaluate(tree, attrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := ev.Evaluate(tree, attrs)
		if err != nil || again != first {
			t.Fatalf("run %d differs: (%v, %v) vs (%v, nil)", i, again, err, first)
		}
	}
	if len(attrs) != 1 || attrs["a"] != int64(1) {
		t.Fatalf("attrs mutated: %v", attrs)
	}
	if tree.Left != trueLeaf {
		t.Fatalf("tree mutated: %+v", tree)
	}
}
