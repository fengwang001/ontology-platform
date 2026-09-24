// Package filter trims rows and predicates according to a column policy.
package filter

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
)

// ErrInvisibleColumn means the predicate read a column the role cannot see.
var ErrInvisibleColumn = errors.New("filter: predicate references invisible column")

// Ref is one rejected column reference with its root-to-node path.
type Ref struct {
	Column string
	Path   []int
}

// Row is one input row.
type Row map[string]string

// Analyzer runs one single traversal per predicate.
type Analyzer struct {
	pol *policy.Policy

	nodesVisited int
	copies       int
}

// NewAnalyzer creates an analyzer bound to a policy.
func NewAnalyzer(pol *policy.Policy) *Analyzer {
	return &Analyzer{pol: pol}
}

// NodesVisited reports how many predicate nodes the last Analyze traversal touched.
func (a *Analyzer) NodesVisited() int { return a.nodesVisited }

// Copies reports key copies performed by the last ProjectRows call.
func (a *Analyzer) Copies() int { return a.copies }

// Analyze performs a single post-order traversal that validates, constant-folds
// and authorizes the predicate simultaneously. It returns the folded tree and
// all surviving invisible references; a non-empty slice implies rejection.
func (a *Analyzer) Analyze(role string, n *predicate.Node) (*predicate.Node, []Ref, error) {
	a.nodesVisited = 0
	if n == nil {
		return nil, nil, nil
	}
	folded, refs, err := a.fold(role, n, nil)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) > 0 {
		return folded, refs, ErrInvisibleColumn
	}
	return folded, nil, nil
}

func (a *Analyzer) fold(role string, n *predicate.Node, path []int) (*predicate.Node, []Ref, error) {
	a.nodesVisited++
	switch n.Op {
	case predicate.Const:
		return predicate.NewConst(n.Bool), nil, nil
	case predicate.Eq, predicate.IsNull:
		if n.Column == "" {
			return nil, nil, predicate.ErrInvalidPredicate
		}
		ok, err := a.pol.Visible(role, n.Column)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			p := append([]int(nil), path...)
			return &predicate.Node{Op: n.Op, Column: n.Column, Value: n.Value}, []Ref{{Column: n.Column, Path: p}}, nil
		}
		return &predicate.Node{Op: n.Op, Column: n.Column, Value: n.Value}, nil, nil
	case predicate.Not:
		if len(n.Kids) != 1 {
			return nil, nil, predicate.ErrInvalidPredicate
		}
		kid, refs, err := a.fold(role, n.Kids[0], appendPath(path, 0))
		if err != nil {
			return nil, nil, err
		}
		if kid.Op == predicate.Const {
			return predicate.NewConst(!kid.Bool), nil, nil
		}
		return predicate.NewNot(kid), refs, nil
	case predicate.And, predicate.Or:
		return a.foldCombinator(role, n, path)
	default:
		return nil, nil, fmt.Errorf("%w: unknown op %d", predicate.ErrInvalidPredicate, n.Op)
	}
}

func (a *Analyzer) foldCombinator(role string, n *predicate.Node, path []int) (*predicate.Node, []Ref, error) {
	if len(n.Kids) < 2 {
		return nil, nil, predicate.ErrInvalidPredicate
	}
	kids := make([]*predicate.Node, 0, len(n.Kids))
	var refs []Ref
	shortVal := false
	shorts := false
	for i, kid := range n.Kids {
		folded, kidRefs, err := a.fold(role, kid, appendPath(path, i))
		if err != nil {
			return nil, nil, err
		}
		isConst := folded.Op == predicate.Const
		if isConst {
			if n.Op == predicate.And && !folded.Bool || n.Op == predicate.Or && folded.Bool {
				shorts = true
				shortVal = folded.Bool
			} else {
				kids = append(kids, folded)
			}
			continue
		}
		kids = append(kids, folded)
		refs = append(refs, kidRefs...)
	}
	if shorts {
		return predicate.NewConst(shortVal), nil, nil
	}
	if len(kids) == 0 {
		v := n.Op == predicate.And
		return predicate.NewConst(v), nil, nil
	}
	if len(kids) == 1 {
		return kids[0], refs, nil
	}
	if n.Op == predicate.And {
		return predicate.NewAnd(kids...), refs, nil
	}
	return predicate.NewOr(kids...), refs, nil
}

// ProjectRows keeps only role-visible columns, counting every key copy.
func (a *Analyzer) ProjectRows(role string, rows []Row) []map[string]string {
	visible := a.pol.VisibleSet(role)
	a.copies = 0
	out := make([]map[string]string, len(rows))
	for i, row := range rows {
		projected := make(map[string]string, len(visible))
		for _, col := range visible {
			if val, ok := row[col]; ok {
				projected[col] = val
				a.copies++
			}
		}
		out[i] = projected
	}
	return out
}

func appendPath(path []int, i int) []int {
	out := make([]int, 0, len(path)+1)
	out = append(out, path...)
	return append(out, i)
}

// Apply is the one-call entry point: predicate authorization plus row trimming.
func Apply(pol *policy.Policy, role string, n *predicate.Node, rows []Row) (*predicate.Node, []Ref, []map[string]string, error) {
	a := NewAnalyzer(pol)
	folded, refs, err := a.Analyze(role, n)
	if err != nil {
		return nil, refs, nil, err
	}
	return folded, nil, a.ProjectRows(role, rows), nil
}
