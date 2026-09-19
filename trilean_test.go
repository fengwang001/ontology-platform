package ontology

import "testing"

var (
	trueLeaf    = Comparison{Op: Eq, Attr: "a", Value: int64(1)}
	falseLeaf   = Comparison{Op: Eq, Attr: "a", Value: int64(2)}
	unknownLeaf = Comparison{Op: Eq, Attr: "missing", Value: int64(1)}
	testAttrs   = map[string]any{"a": int64(1)}
)

func leafFor(t Trilean) Predicate {
	switch t {
	case True:
		return trueLeaf
	case False:
		return falseLeaf
	default:
		return unknownLeaf
	}
}

func evalTree(t *testing.T, pred Predicate) Result {
	t.Helper()
	res, err := NewEvaluator(64).Evaluate(pred, testAttrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func TestKleeneAndTruthTable(t *testing.T) {
	cases := []struct {
		left, right, want Trilean
	}{
		{True, True, True},
		{True, False, False},
		{True, Unknown, Unknown},
		{False, True, False},
		{False, False, False},
		{False, Unknown, False},
		{Unknown, True, Unknown},
		{Unknown, False, False},
		{Unknown, Unknown, Unknown},
	}
	for _, c := range cases {
		if got := And(c.left, c.right); got != c.want {
			t.Errorf("And(%v, %v) = %v, want %v", c.left, c.right, got, c.want)
		}
		res := evalTree(t, AndPred{Left: leafFor(c.left), Right: leafFor(c.right)})
		if res.Value != c.want {
			t.Errorf("eval And(%v, %v) = %v, want %v", c.left, c.right, res.Value, c.want)
		}
	}
}

func TestKleeneOrTruthTable(t *testing.T) {
	cases := []struct {
		left, right, want Trilean
	}{
		{True, True, True},
		{True, False, True},
		{True, Unknown, True},
		{False, True, True},
		{False, False, False},
		{False, Unknown, Unknown},
		{Unknown, True, True},
		{Unknown, False, Unknown},
		{Unknown, Unknown, Unknown},
	}
	for _, c := range cases {
		if got := Or(c.left, c.right); got != c.want {
			t.Errorf("Or(%v, %v) = %v, want %v", c.left, c.right, got, c.want)
		}
		res := evalTree(t, OrPred{Left: leafFor(c.left), Right: leafFor(c.right)})
		if res.Value != c.want {
			t.Errorf("eval Or(%v, %v) = %v, want %v", c.left, c.right, res.Value, c.want)
		}
	}
}

func TestKleeneNotTruthTable(t *testing.T) {
	cases := []struct {
		in, want Trilean
	}{
		{True, False},
		{False, True},
		{Unknown, Unknown},
	}
	for _, c := range cases {
		if got := Not(c.in); got != c.want {
			t.Errorf("Not(%v) = %v, want %v", c.in, got, c.want)
		}
		res := evalTree(t, NotPred{Child: leafFor(c.in)})
		if res.Value != c.want {
			t.Errorf("eval Not(%v) = %v, want %v", c.in, res.Value, c.want)
		}
	}
}
