package ontology

// Evaluator evaluates predicate trees against attribute maps. It is
// stateless and safe for concurrent use; per-evaluation state (such as the
// leaf counter) lives on the stack of each Eval call.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator returns an Evaluator that rejects predicate trees deeper
// than maxDepth with a *DepthError. maxDepth must be positive.
func NewEvaluator(maxDepth int) *Evaluator {
	if maxDepth < 1 {
		maxDepth = 1
	}
	return &Evaluator{maxDepth: maxDepth}
}

// Eval evaluates p against attrs and returns the three-valued result.
// attrs may be nil. Neither p nor attrs is modified.
func (e *Evaluator) Eval(p Pred, attrs map[string]any) (Tri, error) {
	v, _, err := e.EvalCount(p, attrs)
	return v, err
}

// EvalCount is like Eval but also reports how many comparison leaves
// (*Cmp nodes) were actually evaluated, which makes short-circuiting
// observable. IsNull nodes are not comparisons and are not counted.
func (e *Evaluator) EvalCount(p Pred, attrs map[string]any) (Tri, int, error) {
	s := &evalState{attrs: attrs}
	v, err := e.walk(p, 1, s)
	return v, s.leaves, err
}

// evalState carries per-evaluation mutable state.
type evalState struct {
	attrs  map[string]any
	leaves int
}

// walk evaluates p at the given depth (root is depth 1).
func (e *Evaluator) walk(p Pred, depth int, s *evalState) (Tri, error) {
	if depth > e.maxDepth {
		return False, &DepthError{Limit: e.maxDepth}
	}
	switch n := p.(type) {
	case *Cmp:
		s.leaves++
		v, ok := s.attrs[n.Attr]
		if !ok {
			return Unknown, nil
		}
		return compareLeaf(n, v)
	case *IsNullNode:
		if _, ok := s.attrs[n.Attr]; ok {
			return False, nil
		}
		return True, nil
	case *NotNode:
		v, err := e.walk(n.P, depth+1, s)
		if err != nil {
			return False, err
		}
		return Not(v), nil
	case *AndNode:
		return e.walkAnd(n, depth, s)
	case *OrNode:
		return e.walkOr(n, depth, s)
	default:
		return False, &TypeError{Attr: "<tree>", AttrType: typeName(p), LitType: "<predicate>"}
	}
}

// walkAnd evaluates And left to right. False short-circuits; Unknown does
// not. Errors from evaluated subtrees always propagate.
func (e *Evaluator) walkAnd(n *AndNode, depth int, s *evalState) (Tri, error) {
	l, err := e.walk(n.L, depth+1, s)
	if err != nil {
		return False, err
	}
	if l == False {
		return False, nil // short-circuit: R is never evaluated
	}
	r, err := e.walk(n.R, depth+1, s)
	if err != nil {
		return False, err
	}
	return And(l, r), nil
}

// walkOr evaluates Or left to right. True short-circuits; Unknown does not.
func (e *Evaluator) walkOr(n *OrNode, depth int, s *evalState) (Tri, error) {
	l, err := e.walk(n.L, depth+1, s)
	if err != nil {
		return False, err
	}
	if l == True {
		return True, nil // short-circuit: R is never evaluated
	}
	r, err := e.walk(n.R, depth+1, s)
	if err != nil {
		return False, err
	}
	return Or(l, r), nil
}
