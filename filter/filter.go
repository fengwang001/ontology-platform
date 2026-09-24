// Package filter prunes rows and predicates according to a policy. Any
// predicate that, after constant folding, still references a column
// invisible to the role is rejected wholesale with the column names and
// their paths in the predicate tree.
package filter

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/policy"
	"ontology/predicate"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrUnknownRole     = errors.New("unknown role")
	ErrUnknownColumn   = errors.New("unknown column")
	ErrInvisibleColumn = errors.New("invisible column in predicate")
)

// Ref is one reference to a column at a path in the predicate tree.
type Ref struct {
	Column string
	Path   string
}

// InvisibleError reports predicate references to columns the role cannot see.
type InvisibleError struct{ Refs []Ref }

func (e *InvisibleError) Error() string {
	parts := make([]string, len(e.Refs))
	for i, r := range e.Refs {
		parts[i] = r.Column + "@" + r.Path
	}
	return fmt.Sprintf("%s: %s", ErrInvisibleColumn, strings.Join(parts, ", "))
}

// Is matches ErrInvisibleColumn.
func (e *InvisibleError) Is(target error) bool { return target == ErrInvisibleColumn }

// UnknownColumnError reports references to columns absent from the schema.
type UnknownColumnError struct{ Cols []string }

func (e *UnknownColumnError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnknownColumn, strings.Join(e.Cols, ", "))
}

// Is matches ErrUnknownColumn.
func (e *UnknownColumnError) Is(target error) bool { return target == ErrUnknownColumn }

// Engine applies a policy to predicates and rows. It is not safe for
// concurrent use.
type Engine struct {
	pol     *policy.Policy
	schema  map[string]struct{}
	visited int // predicate nodes visited by the last Check
	copies  int // key/value copies made by the last PruneRow
}

// New builds an Engine over the given policy and schema (all column names).
func New(pol *policy.Policy, schema []string) *Engine {
	s := make(map[string]struct{}, len(schema))
	for _, c := range schema {
		s[c] = struct{}{}
	}
	return &Engine{pol: pol, schema: s}
}

// Visited returns how many predicate nodes the last Check visited.
func (e *Engine) Visited() int { return e.visited }

// Copies returns how many key/value pairs the last PruneRow copied.
func (e *Engine) Copies() int { return e.copies }

// Check validates pred for role. A nil pred means "no filter" and always
// passes. The predicate is constant-folded first; references discarded by
// folding were never read and do not cause rejection. The folded tree is
// walked exactly once.
func (e *Engine) Check(role string, pred predicate.Node) error {
	visible, ok := e.pol.Visible(role)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	e.visited = 0
	if pred == nil {
		return nil
	}
	w := &walker{visible: visible, schema: e.schema, byCol: map[string][]string{}}
	w.walk(predicate.Fold(pred), "root")
	e.visited = w.visited
	if len(w.unknown) > 0 {
		return &UnknownColumnError{Cols: w.unknown}
	}
	if len(w.byCol) > 0 {
		refs := make([]Ref, 0, len(w.byCol))
		for _, col := range sortedKeys(w.byCol) {
			for _, p := range w.byCol[col] {
				refs = append(refs, Ref{Column: col, Path: p})
			}
		}
		return &InvisibleError{Refs: refs}
	}
	return nil
}

// PruneRow returns a new map holding only the columns visible to role.
// Invisible columns are removed, never zeroed, so "invisible" stays
// distinguishable from "empty value". Cost is proportional to the number of
// visible columns, independent of the row width.
func (e *Engine) PruneRow(role string, row map[string]any) (map[string]any, error) {
	cols, ok := e.pol.Columns(role)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	e.copies = 0
	out := make(map[string]any, len(cols))
	for _, c := range cols {
		if v, present := row[c]; present {
			out[c] = v
			e.copies++
		}
	}
	return out, nil
}

type walker struct {
	visible map[string]struct{}
	schema  map[string]struct{}
	visited int
	unknown []string
	byCol   map[string][]string
}

func (w *walker) walk(n predicate.Node, path string) {
	w.visited++
	switch t := n.(type) {
	case predicate.Const:
	case predicate.Cmp:
		if _, ok := w.schema[t.Column]; !ok {
			w.unknown = append(w.unknown, t.Column)
			return
		}
		if _, ok := w.visible[t.Column]; !ok {
			w.byCol[t.Column] = append(w.byCol[t.Column], path)
		}
	case predicate.Not:
		w.walk(t.X, path+".not")
	case predicate.And:
		w.walk(t.L, path+".and.left")
		w.walk(t.R, path+".and.right")
	case predicate.Or:
		w.walk(t.L, path+".or.left")
		w.walk(t.R, path+".or.right")
	}
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
