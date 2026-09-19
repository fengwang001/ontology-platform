package ontology

import "testing"

// count runs the tree and returns (value, leavesEvaluated).
func count(t *testing.T, e *Evaluator, p Pred, attrs map[string]any) (Tri, int) {
	t.Helper()
	v, n, err := e.EvalCount(p, attrs)
	if err != nil {
		t.Fatalf("EvalCount returned unexpected error: %v", err)
	}
	return v, n
}

func TestAndShortCircuitsOnFalse(t *testing.T) {
	e := NewEvaluator(16)
	// And(False, <subtree with 3 leaves>): right side must not be touched.
	right := AndP(tLeaf(), OrP(tLeaf(), tLeaf()))
	v, n := count(t, e, AndP(fLeaf(), right), constAttrs)
	if v != False || n != 1 {
		t.Errorf("And(False, ...) = (%s, %d leaves), want (False, 1)", v, n)
	}
}

func TestOrShortCircuitsOnTrue(t *testing.T) {
	e := NewEvaluator(16)
	right := OrP(fLeaf(), AndP(fLeaf(), fLeaf()))
	v, n := count(t, e, OrP(tLeaf(), right), constAttrs)
	if v != True || n != 1 {
		t.Errorf("Or(True, ...) = (%s, %d leaves), want (True, 1)", v, n)
	}
}

func TestUnknownDoesNotShortCircuit(t *testing.T) {
	e := NewEvaluator(16)
	// And(Unknown, False): right side must be evaluated to decide False.
	v, n := count(t, e, AndP(uLeaf(), fLeaf()), constAttrs)
	if v != False || n != 2 {
		t.Errorf("And(Unknown, False) = (%s, %d leaves), want (False, 2)", v, n)
	}
	// Or(Unknown, True): right side must be evaluated to decide True.
	v, n = count(t, e, OrP(uLeaf(), tLeaf()), constAttrs)
	if v != True || n != 2 {
		t.Errorf("Or(Unknown, True) = (%s, %d leaves), want (True, 2)", v, n)
	}
	// And(Unknown, Unknown): both leaves evaluated, result Unknown.
	v, n = count(t, e, AndP(uLeaf(), uLeaf()), constAttrs)
	if v != Unknown || n != 2 {
		t.Errorf("And(Unknown, Unknown) = (%s, %d leaves), want (Unknown, 2)", v, n)
	}
}

func TestLeafCountCoversFullTree(t *testing.T) {
	e := NewEvaluator(16)
	// No short-circuit possible: every leaf is evaluated.
	p := AndP(OrP(fLeaf(), tLeaf()), AndP(tLeaf(), tLeaf()))
	v, n := count(t, e, p, constAttrs)
	if v != True || n != 4 {
		t.Errorf("full tree = (%s, %d leaves), want (True, 4)", v, n)
	}
}

func TestLeafCountExcludesIsNull(t *testing.T) {
	e := NewEvaluator(16)
	// IsNull is a leaf but not a comparison: only the Cmp counts.
	v, n := count(t, e, AndP(IsNull("nope"), tLeaf()), constAttrs)
	if v != True || n != 1 {
		t.Errorf("And(IsNull, True) = (%s, %d leaves), want (True, 1)", v, n)
	}
}

func TestShortCircuitInsideNestedSubtrees(t *testing.T) {
	e := NewEvaluator(16)
	// And(True, And(False, leaf)): the inner And stops after its False
	// left child; total leaves = 1 (outer left) + 1 (inner left) = 2.
	p := AndP(tLeaf(), AndP(fLeaf(), fLeaf()))
	v, n := count(t, e, p, constAttrs)
	if v != False || n != 2 {
		t.Errorf("nested = (%s, %d leaves), want (False, 2)", v, n)
	}
}
