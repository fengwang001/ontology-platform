package predicate

// Evaluator evaluates predicate trees against attribute maps.
// An Evaluator is safe for concurrent use by multiple goroutines.
type Evaluator struct {
	maxDepth int
}

// NewEvaluator builds an Evaluator with the given maximum tree depth.
// maxDepth must be positive.
func NewEvaluator(maxDepth int) *Evaluator {
	return &Evaluator{maxDepth: maxDepth}
}

// MaxDepth returns the configured depth limit.
func (e *Evaluator) MaxDepth() int { return e.maxDepth }

// Eval evaluates n against attrs. Missing attributes produce Unknown.
// leaves counts comparison leaves that were actually evaluated.
// Neither n nor attrs is modified.
func (e *Evaluator) Eval(attrs map[string]any, n Node) (val Tri, leaves int, err error) {
	return Unknown, 0, nil
}
