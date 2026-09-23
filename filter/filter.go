package filter

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var (
	ErrDeniedColumn     = errors.New("predicate references invisible column")
	ErrInvisibleColumn  = errors.New("invisible column requested")
	ErrInvalidPredicate = predicate.ErrInvalidPredicate
)

// Row is one input record keyed by column name.
type Row = map[string]string

// Filter checks predicate visibility and projects rows to granted columns.
type Filter struct {
	policy       policy.Policy
	role         string
	visible      []string
	copies       int
	nodesVisited int
}

// New creates a filter bound to a role policy.
func New(accessPolicy policy.Policy, role string) *Filter {
	return &Filter{policy: accessPolicy, role: role, visible: accessPolicy.VisibleColumns(role)}
}

// NodesVisited returns nodes visited during the latest predicate authorization.
func (f *Filter) NodesVisited() int { return f.nodesVisited }

// Copies returns values copied during the latest row projection.
func (f *Filter) Copies() int { return f.copies }

// Authorize validates and authorizes one predicate without touching rows.
func (f *Filter) Authorize(node *predicate.Node) (*predicate.Node, report.Report, error) {
	f.nodesVisited = 0
	f.copies = 0
	if node == nil {
		return nil, report.New(nil, nil, false), nil
	}
	if err := predicate.Validate(node); err != nil {
		return nil, report.New(nil, nil, false), err
	}
	references := make([]report.Reference, 0)
	simplified := f.analyze(node, "$", &references)
	if len(references) > 0 {
		audit := report.New(nil, references, true)
		return simplified, audit, fmt.Errorf("%w: %s %s", ErrDeniedColumn,
			references[0].Column, references[0].Path)
	}
	return simplified, report.New(nil, nil, false), nil
}

// Apply authorizes the predicate and returns rows containing only visible keys.
func (f *Filter) Apply(node *predicate.Node, rows []Row, schema []string) ([]Row, report.Report, error) {
	simplified, audit, err := f.Authorize(node)
	if err != nil {
		return nil, audit, err
	}
	_ = simplified

	dropped := make(map[string]struct{})
	for _, column := range schema {
		if !f.policy.Visible(f.role, column) {
			dropped[column] = struct{}{}
		}
	}
	projected := make([]Row, len(rows))
	f.copies = 0
	for i, row := range rows {
		projected[i] = make(Row, len(f.visible))
		for _, column := range f.visible {
			value, ok := row[column]
			if !ok {
				continue
			}
			projected[i][column] = value
			f.copies++
		}
	}
	return projected, report.New(dropped, audit.References, false), nil
}

func (f *Filter) analyze(node *predicate.Node, path string, references *[]report.Reference) *predicate.Node {
	f.nodesVisited++
	result := &predicate.Node{Op: node.Op, Bool: node.Bool, Column: node.Column, Value: node.Value}

	switch node.Op {
	case predicate.OpConst, predicate.OpEq, predicate.OpIsNull:
		if node.Op != predicate.OpConst {
			leafPath := path + "/" + string(node.Op)
			if !f.policy.Visible(f.role, node.Column) {
				*references = append(*references, report.Reference{Column: node.Column, Path: leafPath})
			}
		}
	case predicate.OpNot:
		child := f.analyze(node.Children[0], path+"/not", references)
		if child.Op == predicate.OpConst {
			return predicate.Const(!child.Bool)
		}
		result.Children = []*predicate.Node{child}
	case predicate.OpAnd:
		children := f.analyzeChildren(node, path, "and", references)
		if allConst(children) {
			return predicate.Const(evalAnd(children))
		}
		result.Children = children
	case predicate.OpOr:
		return f.analyzeOr(node, path, references)
	}
	return result
}

func (f *Filter) analyzeChildren(node *predicate.Node, path, label string, references *[]report.Reference) []*predicate.Node {
	children := make([]*predicate.Node, len(node.Children))
	for i, child := range node.Children {
		children[i] = f.analyze(child, fmt.Sprintf("%s/%s[%d]", path, label, i), references)
	}
	return children
}

func (f *Filter) analyzeOr(node *predicate.Node, path string, references *[]report.Reference) *predicate.Node {
	start := len(*references)
	children := make([]*predicate.Node, 0, len(node.Children))
	for i, child := range node.Children {
		analyzed := f.analyze(child, fmt.Sprintf("%s/or[%d]", path, i), references)
		if analyzed.Op == predicate.OpConst && analyzed.Bool {
			*references = (*references)[:start]
			for _, remaining := range node.Children[i+1:] {
				f.countOnly(remaining)
			}
			return predicate.Const(true)
		}
		if analyzed.Op == predicate.OpConst {
			continue
		}
		children = append(children, analyzed)
	}
	if len(children) == 0 {
		return predicate.Const(false)
	}
	return &predicate.Node{Op: predicate.OpOr, Children: children}
}

// RequireColumn checks an explicitly requested output column.
func (f *Filter) RequireColumn(column string) error {
	if f.policy.Visible(f.role, column) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvisibleColumn, column)
}

func (f *Filter) countOnly(node *predicate.Node) {
	f.nodesVisited++
	for _, child := range node.Children {
		f.countOnly(child)
	}
}

func allConst(nodes []*predicate.Node) bool {
	for _, node := range nodes {
		if node.Op != predicate.OpConst {
			return false
		}
	}
	return true
}

func evalAnd(nodes []*predicate.Node) bool {
	for _, node := range nodes {
		if !node.Bool {
			return false
		}
	}
	return true
}
