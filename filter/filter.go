// Package filter applies a column-visibility policy to predicates and rows.
//
// Security rule: a predicate that still needs to read any column invisible
// to the role after constant folding is rejected as a whole (see DESIGN.md).
package filter

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
)

var (
	// ErrInvisibleColumn means the predicate reads a column hidden from the role.
	ErrInvisibleColumn = errors.New("predicate references invisible column")
	// ErrUnknownColumn means the predicate names a column outside the universe.
	ErrUnknownColumn = errors.New("predicate references unknown column")
	// ErrUnknownRole means no policy exists for the queried role.
	ErrUnknownRole = errors.New("unknown role")
)

// Rejection records one invisible column and every path it appears at.
type Rejection struct {
	Column string
	Paths  []predicate.Path
}

// ColumnError is ErrUnknownColumn with location.
type ColumnError struct {
	Column string
	Path   predicate.Path
}

func (e *ColumnError) Error() string {
	return fmt.Sprintf("%s: %q at %s", ErrUnknownColumn, e.Column, e.Path)
}
func (e *ColumnError) Is(target error) bool { return target == ErrUnknownColumn }

// RoleError is ErrUnknownRole with the role name.
type RoleError struct{ Role string }

func (e *RoleError) Error() string {
	return fmt.Sprintf("%s: %q", ErrUnknownRole, e.Role)
}
func (e *RoleError) Is(target error) bool { return target == ErrUnknownRole }

// DenyError is ErrInvisibleColumn carrying all offending references.
type DenyError struct{ Rejections []Rejection }

func (e *DenyError) Error() string {
	return fmt.Sprintf("%s: %d column(s)", ErrInvisibleColumn, len(e.Rejections))
}
func (e *DenyError) Is(target error) bool { return target == ErrInvisibleColumn }

// Row is one materialized result row keyed by column name.
type Row map[string]any

// CheckResult is the outcome of one predicate check.
type CheckResult struct {
	Folded     *predicate.Node
	Rejections []Rejection
}

// Analyzer checks predicates and prunes rows for one role.
type Analyzer struct {
	pol          *policy.Policy
	role         string
	nodesVisited int
	copies       int
}

// NewAnalyzer returns an analyzer for the role under the policy.
// It returns ErrUnknownRole if the role was never defined.
func NewAnalyzer(pol *policy.Policy, role string) (*Analyzer, error) {
	if !pol.KnownRole(role) {
		return nil, &RoleError{Role: role}
	}
	return &Analyzer{pol: pol, role: role}, nil
}

// Visited reports how many predicate nodes the last Check traversed.
func (a *Analyzer) Visited() int { return a.nodesVisited }

// Copies reports how many cell assignments row pruning performed.
func (a *Analyzer) Copies() int { return a.copies }

// Check runs the single-pass fold + read analysis.
func (a *Analyzer) Check(root *predicate.Node) (*CheckResult, error) {
	a.nodesVisited = 0
	if root == nil {
		return &CheckResult{}, nil
	}
	rejections := map[string][]predicate.Path{}
	folded, reads, err := a.analyze(root, predicate.Path{}, rejections)
	if err != nil {
		return nil, err
	}
	denied := []Rejection{}
	for column := range reads {
		if !a.pol.Visible(a.role, column) {
			denied = append(denied, Rejection{Column: column, Paths: rejections[column]})
		}
	}
	sortRejections(denied)
	if len(denied) > 0 {
		return &CheckResult{Folded: folded, Rejections: denied}, &DenyError{Rejections: denied}
	}
	return &CheckResult{Folded: folded}, nil
}

func (a *Analyzer) analyze(n *predicate.Node, path predicate.Path, rejects map[string][]predicate.Path) (*predicate.Node, map[string]struct{}, error) {
	a.nodesVisited++
	switch n.Kind {
	case predicate.KindConst:
		return predicate.Const(n.ConstValue), map[string]struct{}{}, nil
	case predicate.KindCmp:
		if !a.pol.HasColumn(n.Column) {
			return nil, nil, &ColumnError{Column: n.Column, Path: appendPath(path)}
		}
		leaf := predicate.Cmp(n.Column, n.Op, n.Value)
		reads := map[string]struct{}{n.Column: {}}
		if !a.pol.Visible(a.role, n.Column) {
			rejects[n.Column] = append(rejects[n.Column], appendPath(path))
		}
		return leaf, reads, nil
	case predicate.KindNot:
		child, reads, err := a.analyze(n.Children[0], append(path, 0), rejects)
		if err != nil {
			return nil, nil, err
		}
		return predicate.Not(child), reads, nil
	}

	foldedChildren := make([]*predicate.Node, len(n.Children))
	childReadSets := make([]map[string]struct{}, len(n.Children))
	// AND short-circuits on FALSE; OR short-circuits on TRUE.
	shortCircuit := n.Kind == predicate.KindAnd
	for i, child := range n.Children {
		folded, childReads, err := a.analyze(child, append(path, i), rejects)
		if err != nil {
			return nil, nil, err
		}
		foldedChildren[i] = folded
		childReadSets[i] = childReads
		if folded.Kind == predicate.KindConst && folded.ConstValue == shortCircuit {
			return predicate.Const(shortCircuit), map[string]struct{}{}, nil
		}
	}
	// Absorbed branches are FALSE under OR and TRUE under AND.
	absorbed := !shortCircuit
	alive := make([]*predicate.Node, 0, len(foldedChildren))
	reads := map[string]struct{}{}
	for i, folded := range foldedChildren {
		if folded.Kind == predicate.KindConst && folded.ConstValue == absorbed {
			continue
		}
		alive = append(alive, folded)
		for column := range childReadSets[i] {
			reads[column] = struct{}{}
		}
	}
	if len(alive) == 0 {
		return predicate.Const(absorbed), map[string]struct{}{}, nil
	}
	if n.Kind == predicate.KindAnd {
		return predicate.And(alive...), reads, nil
	}
	return predicate.Or(alive...), reads, nil
}

// PruneRow returns a new row containing only the role's visible columns.
// Invisible keys are physically absent (never zeroed or set to nil).
func (a *Analyzer) PruneRow(row Row) Row {
	out := Row{}
	for _, column := range a.pol.VisibleList(a.role) {
		if value, ok := row[column]; ok {
			out[column] = value
			a.copies++
		}
	}
	return out
}
