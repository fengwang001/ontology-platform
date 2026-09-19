package ontology

import "reflect"

var float64Type = reflect.TypeOf(float64(0))

// Evaluator evaluates predicate trees against entity property maps using
// Kleene three-valued logic and observable short-circuiting.
//
// An Evaluator holds no per-evaluation state and is safe for concurrent
// use by many goroutines; each evaluation carries its own leaf counter.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator creates an Evaluator. maxDepth bounds predicate-tree
// depth (the root sits at depth 1); a non-positive value defaults to 64.
func NewEvaluator(maxDepth int) *Evaluator {
	if maxDepth <= 0 {
		maxDepth = 64
	}
	return &Evaluator{maxDepth: maxDepth}
}

// Result is the outcome of one evaluation.
type Result struct {
	// Value is the three-valued result; it is only meaningful when Err
	// is nil.
	Value Tri
	// Leaves is the number of comparison leaves that were actually
	// evaluated, including leaves yielding Unknown or type errors.
	Leaves int
}

// Eval evaluates pred against props. It never mutates either argument.
// A type error in any leaf that is actually evaluated aborts the whole
// evaluation; leaves skipped by short-circuiting are never visited and
// therefore cannot raise errors.
func (e *Evaluator) Eval(props map[string]any, pred Predicate) (Result, error) {
	st := &evalState{props: props}
	v, err := e.evalNode(pred, 1, st)
	if err != nil {
		return Result{Leaves: st.leaves}, err
	}
	return Result{Value: v, Leaves: st.leaves}, nil
}

// evalState is per-evaluation scratch space, keeping the shared
// Evaluator free of mutable fields.
type evalState struct {
	props  map[string]any
	leaves int
}

func (e *Evaluator) evalNode(node Predicate, depth int, st *evalState) (Tri, error) {
	if depth > e.maxDepth {
		return Unknown, &DepthError{Limit: e.maxDepth}
	}
	switch n := node.(type) {
	case *Compare:
		return e.evalCompareNode(n, st)
	case *And:
		return e.evalAnd(n.Children, depth, st)
	case *Or:
		return e.evalOr(n.Children, depth, st)
	case *Not:
		return e.evalNot(n.Child, depth, st)
	case *IsNull:
		_, ok := st.props[n.Property]
		return Bool(!ok), nil
	default:
		return Unknown, &NodeError{Node: node}
	}
}

func (e *Evaluator) evalCompareNode(c *Compare, st *evalState) (Tri, error) {
	st.leaves++
	value, ok := st.props[c.Property]
	if !ok {
		return Unknown, nil
	}
	return evalCompare(c.Property, value, c.Op, c.Literal)
}

func (e *Evaluator) evalNot(child Predicate, depth int, st *evalState) (Tri, error) {
	v, err := e.evalNode(child, depth+1, st)
	if err != nil {
		return Unknown, err
	}
	switch v {
	case True:
		return False, nil
	case False:
		return True, nil
	default:
		return Unknown, nil
	}
}
