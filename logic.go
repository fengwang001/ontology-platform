package ontology

// evalAnd implements Kleene conjunction with observable short-circuiting.
//
// A False child stops evaluation immediately (remaining right-hand
// subtrees are never visited). Unknown never short-circuits: it is
// remembered, and evaluation continues so a later False can decide the
// conjunction. The result is False only on a definite False; otherwise
// Unknown if any child was Unknown; otherwise True.
func (e *Evaluator) evalAnd(children []Predicate, depth int, st *evalState) (Tri, error) {
	sawUnknown := false
	for _, child := range children {
		v, err := e.evalNode(child, depth+1, st)
		if err != nil {
			return Unknown, err
		}
		switch v {
		case False:
			return False, nil
		case Unknown:
			sawUnknown = true
		}
	}
	if sawUnknown {
		return Unknown, nil
	}
	return True, nil
}

// evalOr implements Kleene disjunction with observable short-circuiting.
//
// A True child stops evaluation immediately. Unknown never
// short-circuits: evaluation continues so a later True can decide the
// disjunction. The result is True only on a definite True; otherwise
// Unknown if any child was Unknown; otherwise False.
func (e *Evaluator) evalOr(children []Predicate, depth int, st *evalState) (Tri, error) {
	sawUnknown := false
	for _, child := range children {
		v, err := e.evalNode(child, depth+1, st)
		if err != nil {
			return Unknown, err
		}
		switch v {
		case True:
			return True, nil
		case Unknown:
			sawUnknown = true
		}
	}
	if sawUnknown {
		return Unknown, nil
	}
	return False, nil
}
