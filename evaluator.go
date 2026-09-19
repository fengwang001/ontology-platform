package ontology

import "fmt"

// Evaluator evaluates predicate trees against attribute sets.
// It is stateless and safe for concurrent use; per-evaluation
// state (leaf counter) lives on the stack of each Evaluate call.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator returns an Evaluator that rejects trees deeper
// than maxDepth with a *DepthError. The root node is depth 1.
func NewEvaluator(maxDepth int) *Evaluator {
	return &Evaluator{maxDepth: maxDepth}
}

// Result is the outcome of one evaluation.
type Result struct {
	Value Trilean
	// Leaves is the number of Comparison leaves actually evaluated.
	// Leaves skipped by short-circuiting are not counted.
	Leaves int
}

// Evaluate computes the truth value of pred against attrs.
// It does not modify pred or attrs and may be called concurrently.
func (e *Evaluator) Evaluate(pred Predicate, attrs map[string]any) (Result, error) {
	res := &Result{}
	value, err := e.eval(pred, attrs, 1, res)
	if err != nil {
		return Result{}, err
	}
	res.Value = value
	return *res, nil
}

func (e *Evaluator) eval(pred Predicate, attrs map[string]any, depth int, res *Result) (Trilean, error) {
	if depth > e.maxDepth {
		return False, &DepthError{Limit: e.maxDepth}
	}
	switch p := pred.(type) {
	case Comparison:
		res.Leaves++
		return compareLeaf(p, attrs)
	case IsNullPred:
		_, absent := attrs[p.Attr]
		return boolToTrilean(!absent), nil
	case NotPred:
		child, err := e.eval(p.Child, attrs, depth+1, res)
		if err != nil {
			return False, err
		}
		return Not(child), nil
	case AndPred:
		return e.evalAnd(p, attrs, depth, res)
	case OrPred:
		return e.evalOr(p, attrs, depth, res)
	default:
		return False, fmt.Errorf("unknown predicate node type %T", pred)
	}
}

// evalAnd short-circuits on False only. Unknown never short-circuits:
// And(Unknown, False) must evaluate the right side to yield False.
func (e *Evaluator) evalAnd(p AndPred, attrs map[string]any, depth int, res *Result) (Trilean, error) {
	left, err := e.eval(p.Left, attrs, depth+1, res)
	if err != nil {
		return False, err
	}
	if left == False {
		return False, nil
	}
	right, err := e.eval(p.Right, attrs, depth+1, res)
	if err != nil {
		return False, err
	}
	return And(left, right), nil
}

// evalOr short-circuits on True only. Unknown never short-circuits.
func (e *Evaluator) evalOr(p OrPred, attrs map[string]any, depth int, res *Result) (Trilean, error) {
	left, err := e.eval(p.Left, attrs, depth+1, res)
	if err != nil {
		return False, err
	}
	if left == True {
		return True, nil
	}
	right, err := e.eval(p.Right, attrs, depth+1, res)
	if err != nil {
		return False, err
	}
	return Or(left, right), nil
}
