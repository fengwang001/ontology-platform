// Package rules runs a set of rewrite rules to a fixpoint, with iteration
// and visit counters, an iteration bound, and oscillation detection.
package rules

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"ontology/ast"
	"ontology/fold"
)

// Rule is an alias so callers can use rules.Rule interchangeably.
type Rule = fold.Rule

// Stats counts fixpoint work. Rounds is the number of full-tree iterations;
// Visits is the total number of node visits across all rounds; MaxSize is
// the largest tree size seen at the start of any round.
type Stats struct {
	Rounds  int
	Visits  int
	MaxSize int
}

// Std returns the standard rule set (all position-independent fold rules).
func Std() []Rule { return fold.Rules() }

// Random builds a random tree of bounded depth over cols ("t.c" keys),
// for randomized equivalence testing with a fixed seed.
func Random(r *rand.Rand, cols []string, depth int) *ast.Node {
	if depth <= 0 || r.Intn(3) == 0 {
		if r.Intn(4) == 0 {
			return ast.Const(ast.Value(r.Intn(3)))
		}
		parts := strings.SplitN(cols[r.Intn(len(cols))], ".", 2)
		return ast.Col(parts[0], parts[1])
	}
	switch r.Intn(4) {
	case 0:
		return ast.Not(Random(r, cols, depth-1))
	case 1:
		return ast.Eq(Random(r, cols, depth-1), Random(r, cols, depth-1))
	case 2:
		return ast.And(Random(r, cols, depth-1), Random(r, cols, depth-1))
	default:
		return ast.Or(Random(r, cols, depth-1), Random(r, cols, depth-1))
	}
}

// applyOnce rewrites bottom-up: kids first, then each rule in order at n.
func applyOnce(n *ast.Node, rs []Rule, st *Stats) (*ast.Node, bool) {
	st.Visits++
	changed := false
	for i, k := range n.Kids {
		var c bool
		n.Kids[i], c = applyOnce(k, rs, st)
		changed = changed || c
	}
	for _, r := range rs {
		if next, ok := r.Apply(n); ok {
			n = next
			changed = true
		}
	}
	return n, changed
}

// Fixpoint rewrites root until no rule fires. Rules are sorted by name so
// the result does not depend on the order the caller supplied. It fails
// (decidably) on invalid input, when the iteration bound 4*Size(root) is
// exceeded, or when a tree shape repeats (oscillating inverse rules).
func Fixpoint(root *ast.Node, rs []Rule) (*ast.Node, Stats, error) {
	var st Stats
	if err := ast.Validate(root); err != nil {
		return nil, st, err
	}
	sorted := make([]Rule, len(rs))
	copy(sorted, rs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	maxRounds := 4*ast.Size(root) + 1
	seen := map[string]bool{}
	for {
		st.Rounds++
		if sz := ast.Size(root); sz > st.MaxSize {
			st.MaxSize = sz
		}
		if st.Rounds > maxRounds {
			return nil, st, fmt.Errorf("iteration bound %d exceeded", maxRounds)
		}
		key := root.String()
		if seen[key] {
			return nil, st, fmt.Errorf("oscillation detected at round %d: %s", st.Rounds, key)
		}
		seen[key] = true
		var changed bool
		root, changed = applyOnce(root, sorted, &st)
		if next, ok := fold.FoldTop(root); ok {
			st.Visits += ast.Size(root)
			root = next
			changed = true
		}
		if !changed {
			return root, st, nil
		}
	}
}
