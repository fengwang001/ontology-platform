// Package filter validates predicates against a visible-column set and
// prunes rows down to their visible columns.
//
// Policy (see DESIGN.md): a predicate that still references any invisible
// column after constant folding is rejected as a whole, naming every
// offending column and its path in the tree. Rows are pruned by removing
// invisible keys outright, never by zero-filling them.
package filter

import (
	"errors"
	"fmt"
	"strings"

	"ontology/predicate"
)

var (
	// ErrInvisibleColumn marks rejection because the folded predicate still
	// references a column invisible to the caller's role.
	ErrInvisibleColumn = errors.New("filter: predicate references invisible column")
	// ErrInvalidPredicate marks a structurally malformed predicate tree.
	ErrInvalidPredicate = errors.New("filter: invalid predicate")
)

// Ref is one invisible-column reference: the column and its path in the
// predicate tree (root is "$", e.g. "$/or[1]/not").
type Ref struct {
	Col  string
	Path string
}

// RefError aggregates every invisible-column reference found in one pass.
type RefError struct {
	Refs []Ref
}

func (e *RefError) Error() string {
	parts := make([]string, len(e.Refs))
	for i, r := range e.Refs {
		parts[i] = r.Col + "@" + r.Path
	}
	return ErrInvisibleColumn.Error() + ": " + strings.Join(parts, ", ")
}

// Unwrap lets errors.Is(err, ErrInvisibleColumn) match a *RefError.
func (e *RefError) Unwrap() error { return ErrInvisibleColumn }

// Filter checks predicates and prunes rows. The visited and copies counters
// back the complexity guarantees in DESIGN.md section 4; read them through
// Visited and Copies.
type Filter struct {
	visited int
	copies  int
}

// Visited returns how many predicate nodes Check has inspected so far.
func (f *Filter) Visited() int { return f.visited }

// Copies returns how many row entries PruneRow has copied so far.
func (f *Filter) Copies() int { return f.copies }

// Check validates pred against visible in a single traversal of the
// constant-folded tree. A nil predicate means "no filter" and always passes.
// On rejection the returned error is a *RefError matching ErrInvisibleColumn
// and naming every residual invisible reference; structural problems yield
// ErrInvalidPredicate.
func (f *Filter) Check(pred *predicate.Node, visible map[string]bool) error {
	if pred == nil {
		return nil
	}
	var refs []Ref
	if err := f.walk(predicate.Fold(pred), visible, "$", &refs); err != nil {
		return err
	}
	if len(refs) > 0 {
		return &RefError{Refs: refs}
	}
	return nil
}

func (f *Filter) walk(n *predicate.Node, visible map[string]bool, path string, refs *[]Ref) error {
	f.visited++
	switch n.Kind {
	case predicate.Const:
		if _, ok := n.Val.(bool); !ok {
			return invalidf(path, "constant value is not a bool")
		}
	case predicate.Eq, predicate.IsNull:
		if n.Col == "" {
			return invalidf(path, "empty column name")
		}
		if !visible[n.Col] {
			*refs = append(*refs, Ref{Col: n.Col, Path: path})
		}
	case predicate.Not:
		if n.Kid == nil {
			return invalidf(path, "NOT without operand")
		}
		return f.walk(n.Kid, visible, path+"/not", refs)
	case predicate.And, predicate.Or:
		if len(n.Kids) == 0 {
			return invalidf(path, "junction without children")
		}
		name := "and"
		if n.Kind == predicate.Or {
			name = "or"
		}
		for i, kid := range n.Kids {
			if kid == nil {
				return invalidf(path, "nil child")
			}
			if err := f.walk(kid, visible, fmt.Sprintf("%s/%s[%d]", path, name, i), refs); err != nil {
				return err
			}
		}
	default:
		return invalidf(path, "unknown node kind")
	}
	return nil
}

func invalidf(path, why string) error {
	return fmt.Errorf("%w at %s: %s", ErrInvalidPredicate, path, why)
}

// PruneRow returns a new map holding only the visible columns of row.
// Invisible columns are removed outright, never zero-valued, so "invisible"
// stays distinguishable from "empty". The returned slice lists the dropped
// columns in first-seen order. Copies grow by exactly one per visible entry,
// independent of the total column count.
func (f *Filter) PruneRow(row map[string]any, visible map[string]bool) (map[string]any, []string) {
	out := make(map[string]any, len(visible))
	var dropped []string
	for col, val := range row {
		if visible[col] {
			out[col] = val
			f.copies++
		} else {
			dropped = append(dropped, col)
		}
	}
	return out, dropped
}
