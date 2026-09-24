// Package filter enforces column-level visibility: it checks predicates
// against a role's visible columns in a single left-to-right pass with
// constant folding, and prunes rows down to visible columns only.
// A predicate referencing any invisible column is rejected as a whole
// (DESIGN.md, 丙); a subtree skipped by short-circuit folding is never
// "read", so invisible columns inside it do not trigger rejection.
package filter

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

// The three distinguishable failure classes of a predicate check.
var (
	ErrInvisibleColumn  = errors.New("filter: predicate reads a column invisible to the role")
	ErrUnknownColumn    = errors.New("filter: predicate reads an unknown column")
	ErrInvalidPredicate = errors.New("filter: invalid predicate")
)

// RefError carries every invisible-column reference found in one pass.
// It unwraps to ErrInvisibleColumn.
type RefError struct{ Refs []report.Ref }

func (e *RefError) Error() string {
	return fmt.Sprintf("%s: %v", ErrInvisibleColumn, e.Refs)
}

// Unwrap exposes ErrInvisibleColumn to errors.Is.
func (e *RefError) Unwrap() error { return ErrInvisibleColumn }

// Stats exposes the engine's execution counters. The counters
// themselves are unexported fields, reset only via ResetStats.
type Stats struct {
	VisitedNodes int // predicate nodes visited during Check
	CopiedCells  int // row cells copied during Prune
}

// Engine checks predicates and prunes rows against a policy.
type Engine struct {
	pol     *policy.Policy
	visited int
	copies  int
}

// NewEngine returns an Engine enforcing pol.
func NewEngine(pol *policy.Policy) *Engine { return &Engine{pol: pol} }

// Stats returns a snapshot of the counters.
func (e *Engine) Stats() Stats {
	return Stats{VisitedNodes: e.visited, CopiedCells: e.copies}
}

// ResetStats zeroes the counters.
func (e *Engine) ResetStats() { e.visited, e.copies = 0, 0 }

// Check validates root for role in a single left-to-right pass with
// constant folding. A nil root means "no filter" and is allowed. If
// any evaluated subtree references an invisible column, Check returns
// a *RefError listing every such reference.
func (e *Engine) Check(role string, root predicate.Node) error {
	if root == nil {
		return nil
	}
	vis := e.pol.Visible(role)
	var refs []report.Ref
	if _, _, err := e.check(root, "$", vis, &refs); err != nil {
		return err
	}
	if len(refs) > 0 {
		return &RefError{Refs: refs}
	}
	return nil
}

// check visits one node and returns (isConst, value) when the subtree
// folds to a boolean constant.
func (e *Engine) check(n predicate.Node, path string, vis policy.Set, refs *[]report.Ref) (bool, bool, error) {
	e.visited++
	switch t := n.(type) {
	case nil:
		return false, false, fmt.Errorf("%w: nil node at %s", ErrInvalidPredicate, path)
	case predicate.Const:
		return true, t.Value, nil
	case predicate.Compare:
		return false, false, e.column(t.Column, path, vis, refs)
	case predicate.IsNull:
		return false, false, e.column(t.Column, path, vis, refs)
	case predicate.Not:
		isConst, val, err := e.check(t.Child, path+".not", vis, refs)
		if err != nil || !isConst {
			return isConst, val, err
		}
		return true, !val, nil
	case predicate.And:
		return e.chain(t.Children, path+".and", true, vis, refs)
	case predicate.Or:
		return e.chain(t.Children, path+".or", false, vis, refs)
	default:
		return false, false, fmt.Errorf("%w: node type %T", ErrInvalidPredicate, n)
	}
}

// column records an invisible reference or fails on an unknown column.
func (e *Engine) column(col, path string, vis policy.Set, refs *[]report.Ref) error {
	if !e.pol.Known(col) {
		return fmt.Errorf("%w: %s at %s", ErrUnknownColumn, col, path)
	}
	if !vis[col] {
		*refs = append(*refs, report.Ref{Column: col, Path: path})
	}
	return nil
}

// chain folds And (isAnd=true) / Or children left to right. A child
// that decides the junction (false for And, true for Or) short-circuits:
// the remaining children are never visited, hence never "read".
func (e *Engine) chain(kids []predicate.Node, path string, isAnd bool, vis policy.Set, refs *[]report.Ref) (bool, bool, error) {
	if len(kids) == 0 {
		return false, false, fmt.Errorf("%w: empty junction at %s", ErrInvalidPredicate, path)
	}
	sawValue := false // any child that did not fold to a constant
	for i, kid := range kids {
		isConst, val, err := e.check(kid, fmt.Sprintf("%s[%d]", path, i), vis, refs)
		if err != nil {
			return false, false, err
		}
		if !isConst {
			sawValue = true
			continue
		}
		if isAnd && !val {
			return true, false, nil
		}
		if !isAnd && val {
			return true, true, nil
		}
	}
	if !sawValue {
		return true, isAnd, nil // all-constant And is true, all-constant Or is false
	}
	return false, false, nil
}

// Prune returns a new row holding only role-visible columns. Invisible
// columns are removed outright (never zero-valued), so "invisible"
// stays distinguishable from "present but empty".
func (e *Engine) Prune(role string, row map[string]any) map[string]any {
	vis := e.pol.Visible(role)
	out := make(map[string]any, len(vis))
	for col, val := range row {
		if vis[col] {
			out[col] = val
			e.copies++
		}
	}
	return out
}

// Execute checks the predicate and, when it is allowed, prunes rows.
// The report records the outcome either way; a rejected query returns
// no rows.
func (e *Engine) Execute(role string, root predicate.Node, rows []map[string]any) (report.Report, []map[string]any, error) {
	rep := report.Report{}
	err := e.Check(role, root)
	var refErr *RefError
	switch {
	case errors.As(err, &refErr):
		rep.Rejected = true
		rep.Refs = refErr.Refs
	case err != nil:
		rep.Rejected = true
	}
	if rep.Rejected {
		rep.Normalize()
		return rep, nil, err
	}
	vis := e.pol.Visible(role)
	dropped := map[string]bool{}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		for col := range row {
			if !vis[col] {
				dropped[col] = true
			}
		}
		out = append(out, e.Prune(role, row))
	}
	for col := range dropped {
		rep.Dropped = append(rep.Dropped, col)
	}
	rep.Normalize()
	return rep, out, nil
}
