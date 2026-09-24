// Package filter checks predicates and prunes rows against a policy.
//
// Policy decision (see DESIGN.md): any reference to an invisible column
// that survives constant folding rejects the whole query with a
// decidable error naming the column and its path in the predicate tree.
package filter

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/policy"
	"ontology/predicate"
)

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrInvisibleColumn  = errors.New("filter: predicate references invisible column")
	ErrUnknownRole      = errors.New("filter: unknown role")
	ErrInvalidPredicate = errors.New("filter: invalid predicate")
)

// Ref records one invisible column reference and every path at which
// it occurs in the (folded) predicate tree.
type Ref struct {
	Col   string
	Paths []string
}

// Engine applies a policy to predicates and rows. Counters are
// unexported and exposed read-only via Visited and Copies.
type Engine struct {
	pol     policy.Policy
	visited int
	copies  int
}

// NewEngine returns an Engine enforcing pol.
func NewEngine(pol policy.Policy) *Engine { return &Engine{pol: pol} }

// Visited returns the number of predicate nodes visited by the last Check.
func (e *Engine) Visited() int { return e.visited }

// Copies returns the number of column values copied by the last PruneRow.
func (e *Engine) Copies() int { return e.copies }

// Check constant-folds p, then walks the folded tree exactly once.
// A nil predicate means "no filter" and is always allowed.
// It returns the invisible column references (nil when the query is
// allowed) and an error wrapping ErrInvisibleColumn when any exist.
func (e *Engine) Check(role string, p *predicate.Node) ([]Ref, error) {
	if !e.pol.HasRole(role) {
		return nil, ErrUnknownRole
	}
	if p == nil {
		return nil, nil
	}
	e.visited = 0
	refs := map[string][]string{}
	if err := e.walk(role, predicate.Fold(p), "", refs); err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]Ref, 0, len(refs))
	cols := make([]string, 0, len(refs))
	for col, paths := range refs {
		sort.Strings(paths)
		out = append(out, Ref{Col: col, Paths: paths})
		cols = append(cols, col)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Col < out[j].Col })
	sort.Strings(cols)
	return out, fmt.Errorf("%w: %s", ErrInvisibleColumn, strings.Join(cols, ","))
}

// walk visits each node of the folded tree once, counting visits and
// collecting references to invisible columns. path is the path of n.
func (e *Engine) walk(role string, n *predicate.Node, path string, refs map[string][]string) error {
	if n == nil {
		return ErrInvalidPredicate
	}
	e.visited++
	switch n.Kind {
	case predicate.Const:
	case predicate.Cmp:
		if !e.pol.Visible(role, n.Col) {
			refs[n.Col] = append(refs[n.Col], join(path, "CMP"))
		}
	case predicate.And, predicate.Or:
		name := "AND"
		if n.Kind == predicate.Or {
			name = "OR"
		}
		self := join(path, name)
		for i, c := range n.Children {
			if err := e.walk(role, c, fmt.Sprintf("%s[%d]", self, i), refs); err != nil {
				return err
			}
		}
	case predicate.Not:
		if err := e.walk(role, n.Child, join(path, "NOT"), refs); err != nil {
			return err
		}
	default:
		return ErrInvalidPredicate
	}
	return nil
}

func join(parent, self string) string {
	if parent == "" {
		return self
	}
	return parent + "/" + self
}

// PruneRow returns the row restricted to columns visible to role, plus
// the sorted names of the removed columns. Invisible columns are deleted
// outright (never zero-valued), so "invisible" stays distinguishable
// from "empty". Cost is O(visible columns): the visible set is iterated
// and looked up in the row, independent of the row's total width.
func (e *Engine) PruneRow(role string, row map[string]any) (map[string]any, []string, error) {
	if !e.pol.HasRole(role) {
		return nil, nil, ErrUnknownRole
	}
	vis := e.pol.Columns(role)
	e.copies = 0
	kept := make(map[string]any, len(vis))
	for col := range vis {
		if v, ok := row[col]; ok {
			kept[col] = v
			e.copies++
		}
	}
	var pruned []string
	for col := range row {
		if !vis[col] {
			pruned = append(pruned, col)
		}
	}
	sort.Strings(pruned)
	return kept, pruned, nil
}
