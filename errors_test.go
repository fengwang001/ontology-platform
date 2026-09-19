package ontology

import (
	"errors"
	"testing"
)

// badLeaf is a comparison whose operands are never comparable
// (string attribute vs int64 literal), so it always raises *TypeError
// when — and only when — it is actually evaluated.
func badLeaf() Pred { return EqTo("s", int64(1)) }

var strAttrs = map[string]any{"s": "text", "x": int64(1)}

func TestErrorInSkippedSubtreeIsNotReported(t *testing.T) {
	e := NewEvaluator(16)
	// And(False, badLeaf): short-circuit skips the erroneous leaf.
	v, n, err := e.EvalCount(AndP(fLeaf(), badLeaf()), strAttrs)
	if err != nil || v != False || n != 1 {
		t.Errorf("And(False, bad) = (%s, %d, %v), want (False, 1, nil)", v, n, err)
	}
	// Or(True, badLeaf): same on the Or side.
	v, n, err = e.EvalCount(OrP(tLeaf(), badLeaf()), strAttrs)
	if err != nil || v != True || n != 1 {
		t.Errorf("Or(True, bad) = (%s, %d, %v), want (True, 1, nil)", v, n, err)
	}
}

func TestErrorInEvaluatedSubtreePropagates(t *testing.T) {
	e := NewEvaluator(16)
	// And(Unknown, badLeaf): Unknown forces evaluation of the right side;
	// the error must not be masked by the And result.
	_, n, err := e.EvalCount(AndP(uLeaf(), badLeaf()), strAttrs)
	var te *TypeError
	if !errors.As(err, &te) {
		t.Errorf("And(Unknown, bad): err = %v, want *TypeError", err)
	}
	if n != 2 {
		t.Errorf("And(Unknown, bad): %d leaves, want 2", n)
	}
	// Error on the left side aborts before the right side is touched.
	_, n, err = e.EvalCount(AndP(badLeaf(), tLeaf()), strAttrs)
	if !errors.As(err, &te) || n != 1 {
		t.Errorf("And(bad, True) = (%d leaves, %v), want (1, *TypeError)", n, err)
	}
	// Or(False, badLeaf): False does not short-circuit Or, error surfaces.
	_, _, err = e.EvalCount(OrP(fLeaf(), badLeaf()), strAttrs)
	if !errors.As(err, &te) {
		t.Errorf("Or(False, bad): err = %v, want *TypeError", err)
	}
	// Errors propagate through Not as well.
	_, err = e.Eval(NotP(badLeaf()), strAttrs)
	if !errors.As(err, &te) {
		t.Errorf("Not(bad): err = %v, want *TypeError", err)
	}
}

func TestDepthLimitIsDecidableError(t *testing.T) {
	e := NewEvaluator(3)
	// Build a chain of Not nodes deeper than the limit.
	var p Pred = tLeaf()
	for i := 0; i < 5; i++ {
		p = NotP(p)
	}
	_, err := e.Eval(p, constAttrs)
	var de *DepthError
	if !errors.As(err, &de) {
		t.Fatalf("deep tree: err = %v, want *DepthError", err)
	}
	if de.Limit != 3 {
		t.Errorf("DepthError.Limit = %d, want 3", de.Limit)
	}
	// A tree exactly at the limit must succeed (leaves at depth 3).
	e3 := NewEvaluator(3)
	ok := AndP(AndP(tLeaf(), tLeaf()), tLeaf())
	if got := evalOK(t, e3, ok, constAttrs); got != True {
		t.Errorf("tree at depth limit = %s, want True", got)
	}
}

func TestErrorDoesNotCorruptEvaluator(t *testing.T) {
	e := NewEvaluator(16)
	_, _ = e.Eval(AndP(uLeaf(), badLeaf()), strAttrs)
	// The same evaluator keeps working normally afterwards.
	if got := evalOK(t, e, tLeaf(), constAttrs); got != True {
		t.Errorf("evaluator corrupted after error: got %s, want True", got)
	}
}
