// Package audit self-checks that splitting a budgeted traversal into two
// segments yields exactly the same visited sequence as a single run.
package audit

import (
	"bytes"
	"fmt"
	"slices"

	"ontology/graph"
	"ontology/walk"
)

// CheckSplits walks start with the full budget, then replays the
// traversal as two segments (a, budget-a) for every split point
// a in [1, budget-1]. It returns an error naming the first split point
// whose concatenated sequence or final token differs from the one-shot
// result, and nil if all split points are equivalent.
func CheckSplits(g *graph.Graph, start string, budget int) error {
	full, err := walk.Walk(g, start, budget)
	if err != nil {
		return fmt.Errorf("audit: one-shot walk: %w", err)
	}
	for a := 1; a < budget; a++ {
		first, err := walk.Walk(g, start, a)
		if err != nil {
			return fmt.Errorf("audit: split %d: first segment: %w", a, err)
		}
		second, err := walk.Resume(g, first.Token, budget-a)
		if err != nil {
			return fmt.Errorf("audit: split %d: second segment: %w", a, err)
		}
		joined := append(slices.Clone(first.Seq), second.Seq...)
		if !slices.Equal(joined, full.Seq) {
			return fmt.Errorf("audit: split %d: sequence %v != one-shot %v", a, joined, full.Seq)
		}
		if !bytes.Equal(second.Token, full.Token) {
			return fmt.Errorf("audit: split %d: final token differs from one-shot", a)
		}
	}
	return nil
}
