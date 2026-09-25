package ontology

import "fmt"

// Evaluator evaluates predicate trees against property sets. It is
// safe for concurrent use; each call to NewResult obtains an
// independent result handle with its own leaf counter.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator creates an Evaluator. maxDepth limits the depth of
// predicate trees; deeper trees yield a *DepthError. A maxDepth <= 0
// is treated as 1 (only a single leaf node is allowed).
func NewEvaluator(maxDepth int) *Evaluator {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	return &Evaluator{maxDepth: maxDepth}
}

// Result carries the outcome of a single evaluation, including the
// number of leaf comparisons actually performed.
type Result struct {
	leaves int
}

// LeafCount returns how many leaf comparisons were actually evaluated
// during the last Eval call on this Result.
func (r *Result) LeafCount() int {
	return r.leaves
}

// NewResult returns a fresh result handle bound to this evaluator.
func (e *Evaluator) NewResult() *Result {
	return &Result{}
}

// Eval evaluates the predicate tree against props and stores the leaf
// count in r. It returns the three-valued result or a decidable error
// (*TypeError or *DepthError).
func (e *Evaluator) Eval(p *Predicate, props map[string]any, r *Result) (Trilean, error) {
	if r == nil {
		r = &Result{}
	}
	r.leaves = 0
	return e.eval(p, props, r, 1)
}

// eval is the recursive worker; depth starts at 1 for the root.
func (e *Evaluator) eval(p *Predicate, props map[string]any, r *Result, depth int) (Trilean, error) {
	if depth > e.maxDepth {
		return False, &DepthError{MaxDepth: e.maxDepth}
	}
	switch p.Kind {
	case KindCompare:
		r.leaves++
		v, ok := props[p.Attr]
		if !ok {
			return Unknown, nil
		}
		return compareLeaf(p.Attr, p.Op, v, p.Value)
	case KindIsNull:
		_, ok := props[p.Attr]
		return boolResult(!ok), nil
	case KindNot:
		if len(p.Children) != 1 {
			return False, fmt.Errorf("ontology: Not node requires exactly 1 child, got %d", len(p.Children))
		}
		v, err := e.eval(p.Children[0], props, r, depth+1)
		if err != nil {
			return False, err
		}
		return Not(v), nil
	case KindAnd:
		return e.evalAnd(p, props, r, depth)
	case KindOr:
		return e.evalOr(p, props, r, depth)
	default:
		return False, fmt.Errorf("ontology: unknown predicate kind %d", int(p.Kind))
	}
}

// evalAnd evaluates children left to right. False short-circuits
// immediately; Unknown does not. A type error in any evaluated child
// aborts the whole evaluation.
func (e *Evaluator) evalAnd(p *Predicate, props map[string]any, r *Result, depth int) (Trilean, error) {
	result := True
	for _, child := range p.Children {
		v, err := e.eval(child, props, r, depth+1)
		if err != nil {
			return False, err
		}
		if v == False {
			return False, nil
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result, nil
}

// evalOr evaluates children left to right. True short-circuits
// immediately; Unknown does not. A type error in any evaluated child
// aborts the whole evaluation.
func (e *Evaluator) evalOr(p *Predicate, props map[string]any, r *Result, depth int) (Trilean, error) {
	result := False
	for _, child := range p.Children {
		v, err := e.eval(child, props, r, depth+1)
		if err != nil {
			return False, err
		}
		if v == True {
			return True, nil
		}
		if v == Unknown {
			result = Unknown
		}
	}
	return result, nil
}
