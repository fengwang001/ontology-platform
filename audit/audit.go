// Package audit self-checks that splitting a budgeted traversal into two
// resumed segments yields exactly the same visited sequence as one run.
package audit

import (
	"fmt"
	"slices"

	"ontology/graph"
	"ontology/walk"
)

// RunOnce visits up to budget nodes from start in a single traversal.
func RunOnce(g *graph.Graph, start string, budget int) ([]string, error) {
	res, err := walk.Walk(g, walk.Initial(start), budget)
	if err != nil {
		return nil, err
	}
	return res.Visited, nil
}

// RunSplit visits budget a, then resumes the checkpoint with budget n-a,
// returning the concatenation of both visited sequences.
func RunSplit(g *graph.Graph, start string, n, a int) ([]string, error) {
	first, err := walk.Walk(g, walk.Initial(start), a)
	if err != nil {
		return nil, err
	}
	second, err := walk.Walk(g, first.Next, n-a)
	if err != nil {
		return nil, err
	}
	return append(first.Visited, second.Visited...), nil
}

// Equivalent verifies every split point a in [1, n-1]: the two-segment
// concatenation must equal the single-run sequence element by element.
// It returns nil on success or an error naming the first failing split.
func Equivalent(g *graph.Graph, start string, n int) error {
	once, err := RunOnce(g, start, n)
	if err != nil {
		return err
	}
	for a := 1; a < n; a++ {
		joined, err := RunSplit(g, start, n, a)
		if err != nil {
			return fmt.Errorf("audit: split %d/%d: %w", a, n, err)
		}
		if !slices.Equal(joined, once) {
			return fmt.Errorf("audit: split %d/%d: got %v, want %v", a, n, joined, once)
		}
	}
	return nil
}
