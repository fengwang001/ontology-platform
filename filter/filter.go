// Package filter enforces column visibility on predicates and rows.
//
// A predicate that references any column invisible to the role is rejected
// as a whole (design approach 丙): nothing is evaluated and no rows are
// returned. Constant folding runs first, so references eliminated by
// folding (e.g. TRUE OR secret = 1) are never read and do not reject.
package filter

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/policy"
	"ontology/predicate"
)

var (
	// ErrUnknownRole marks a role with no grant entry.
	ErrUnknownRole = errors.New("filter: unknown role")
	// ErrInvisibleColumn marks a predicate reading an invisible column.
	ErrInvisibleColumn = errors.New("filter: predicate reads invisible column")
	// ErrInvalidPredicate marks a malformed predicate tree.
	ErrInvalidPredicate = errors.New("filter: invalid predicate")
)

// Ref pinpoints one invisible column and every path where it occurs.
// Paths start at "$"; ".N" enters a NOT, ".L"/".R" enter AND/OR children.
type Ref struct {
	Column string
	Paths  []string
}

// RejectError lists every invisible column reference found in one pass.
type RejectError struct{ Refs []Ref }

func (e *RejectError) Error() string {
	parts := make([]string, 0, len(e.Refs))
	for _, r := range e.Refs {
		parts = append(parts, fmt.Sprintf("%s at %s", r.Column, strings.Join(r.Paths, ",")))
	}
	return ErrInvisibleColumn.Error() + ": " + strings.Join(parts, "; ")
}

// Is reports ErrInvisibleColumn.
func (e *RejectError) Is(target error) bool { return target == ErrInvisibleColumn }

// Checker validates predicates against a policy in a single traversal.
type Checker struct{ visited int }

// Visited returns how many predicate nodes the last Check walked.
func (c *Checker) Visited() int { return c.visited }

// Check folds p, then walks it once. A nil predicate (no filter) passes.
func (c *Checker) Check(p predicate.Pred, pol *policy.Policy, role string) error {
	if !pol.Known(role) {
		return ErrUnknownRole
	}
	c.visited = 0
	if p == nil {
		return nil
	}
	refs := map[string][]string{}
	if err := c.walk(predicate.Fold(p), pol, role, "$", refs); err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	cols := make([]string, 0, len(refs))
	for col := range refs {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	out := make([]Ref, 0, len(cols))
	for _, col := range cols {
		sort.Strings(refs[col])
		out = append(out, Ref{Column: col, Paths: refs[col]})
	}
	return &RejectError{Refs: out}
}

func (c *Checker) walk(p predicate.Pred, pol *policy.Policy, role, path string, refs map[string][]string) error {
	c.visited++
	switch n := p.(type) {
	case predicate.Const:
		return nil
	case predicate.Compare:
		if !pol.Visible(role, n.Column) {
			refs[n.Column] = append(refs[n.Column], path)
		}
		return nil
	case predicate.IsNull:
		if !pol.Visible(role, n.Column) {
			refs[n.Column] = append(refs[n.Column], path)
		}
		return nil
	case predicate.Not:
		if n.Inner == nil {
			return ErrInvalidPredicate
		}
		return c.walk(n.Inner, pol, role, path+".N", refs)
	case predicate.And:
		if n.L == nil || n.R == nil {
			return ErrInvalidPredicate
		}
		if err := c.walk(n.L, pol, role, path+".L", refs); err != nil {
			return err
		}
		return c.walk(n.R, pol, role, path+".R", refs)
	case predicate.Or:
		if n.L == nil || n.R == nil {
			return ErrInvalidPredicate
		}
		if err := c.walk(n.L, pol, role, path+".L", refs); err != nil {
			return err
		}
		return c.walk(n.R, pol, role, path+".R", refs)
	}
	return ErrInvalidPredicate
}

// Pruner removes invisible columns from rows.
type Pruner struct{ copies int }

// Copies returns how many key/value pairs PruneRow has copied so far.
func (p *Pruner) Copies() int { return p.copies }

// PruneRow returns a new map holding only the role's visible columns.
// Invisible columns are removed, never zeroed. Cost is proportional to
// the visible set size, independent of len(row).
func (p *Pruner) PruneRow(row map[string]string, pol *policy.Policy, role string) (map[string]string, error) {
	if !pol.Known(role) {
		return nil, ErrUnknownRole
	}
	out := make(map[string]string, len(row))
	for _, col := range pol.VisibleSet(role) {
		if v, ok := row[col]; ok {
			out[col] = v
			p.copies++
		}
	}
	return out, nil
}
