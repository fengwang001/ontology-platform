package predicate

import "fmt"

// Evaluator evaluates predicate trees against entity attribute maps.
//
// An Evaluator holds configuration only (the depth limit); it is safe for
// concurrent use by many goroutines. Per-evaluation state, in particular the
// count of leaf comparisons actually performed, is local to each Eval call.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator creates an Evaluator with the given maximum predicate depth.
// A leaf node has depth 1; depth <= 0 is rejected.
func NewEvaluator(maxDepth int) (*Evaluator, error) {
	if maxDepth <= 0 {
		return nil, fmt.Errorf("predicate: maxDepth must be positive, got %d", maxDepth)
	}
	return &Evaluator{maxDepth: maxDepth}, nil
}

// Eval evaluates root against attrs. It returns the three-valued result and
// the number of leaf comparisons actually evaluated. Eval is pure: it never
// mutates root or attrs, and repeated calls with equal inputs yield equal
// results and counts.
//
// A type error in an evaluated leaf aborts the whole evaluation; leaves in
// short-circuited-away subtrees are never visited and therefore cannot error.
func (e *Evaluator) Eval(root Node, attrs map[string]any) (Tri, int, error) {
	ev := &evalState{maxDepth: e.maxDepth, attrs: attrs}
	res, err := ev.walk(root, 1)
	if err != nil {
		return Unknown, ev.leaves, err
	}
	return res, ev.leaves, nil
}

type evalState struct {
	attrs    map[string]any
	maxDepth int
	leaves   int
}

func (s *evalState) walk(n Node, depth int) (Tri, *EvalError) {
	if depth > s.maxDepth {
		return Unknown, depthExceeded(s.maxDepth, depth)
	}
	switch node := n.(type) {
	case *Compare:
		s.leaves++
		return compareLeaf(s.attrs, node)
	case *IsNull:
		// IsNull is not a comparison leaf and is not counted.
		if _, ok := s.attrs[node.Attr]; ok {
			return False, nil
		}
		return True, nil
	case *Not:
		if node.Child == nil {
			return Unknown, malformed("Not has nil child")
		}
		v, err := s.walk(node.Child, depth+1)
		if err != nil {
			return Unknown, err
		}
		return KleeneNot(v), nil
	case *AndOr:
		return s.walkAndOr(node, depth)
	default:
		return Unknown, malformed("unknown node type")
	}
}

func (s *evalState) walkAndOr(node *AndOr, depth int) (Tri, *EvalError) {
	if len(node.Children) == 0 {
		return Unknown, malformed("And/Or has no children")
	}
	sawUnknown := false
	for _, child := range node.Children {
		if child == nil {
			return Unknown, malformed("And/Or has nil child")
		}
		v, err := s.walk(child, depth+1)
		if err != nil {
			return Unknown, err
		}
		switch node.Kind {
		case And:
			if v == False {
				return False, nil
			}
		case Or:
			if v == True {
				return True, nil
			}
		default:
			return Unknown, malformed("unknown boolean operator")
		}
		if v == Unknown {
			sawUnknown = true
		}
	}
	if sawUnknown {
		return Unknown, nil
	}
	if node.Kind == And {
		return True, nil
	}
	return False, nil
}
