// Package push pushes top-level conjuncts of a predicate down a plan tree
// of Scan/Join nodes, then self-checks that no scan predicate references
// a foreign table.
package push

import (
	"fmt"
	"sort"
	"strings"

	"ontology/ast"
)

// Plan is a scan of one table or a join of two sub-plans. Pred accumulates
// the conjuncts pushed to this node.
type Plan struct {
	Table string // scan: table name; join: ""
	Pred  *ast.Node
	Left  *Plan
	Right *Plan
}

// Scan makes a leaf plan node.
func Scan(table string) *Plan { return &Plan{Table: table} }

// Join makes an inner-join plan node.
func Join(l, r *Plan) *Plan { return &Plan{Left: l, Right: r} }

// Tables returns the set of tables under p.
func (p *Plan) Tables() map[string]bool {
	if p == nil {
		return nil
	}
	if p.Table != "" {
		return map[string]bool{p.Table: true}
	}
	out := p.Left.Tables()
	for t := range p.Right.Tables() {
		out[t] = true
	}
	return out
}

func subset(set, of map[string]bool) bool {
	for k := range set {
		if !of[k] {
			return false
		}
	}
	return true
}

func (p *Plan) attach(c *ast.Node) {
	if p.Pred == nil {
		p.Pred = c
		return
	}
	p.Pred = ast.And(p.Pred, c)
}

func (p *Plan) place(c *ast.Node, tabs map[string]bool) error {
	if p.Table != "" {
		if !subset(tabs, map[string]bool{p.Table: true}) {
			return fmt.Errorf("conjunct %s does not belong to scan %s", c, p.Table)
		}
		p.attach(c)
		return nil
	}
	switch {
	case subset(tabs, p.Left.Tables()):
		return p.Left.place(c, tabs)
	case subset(tabs, p.Right.Tables()):
		return p.Right.place(c, tabs)
	default:
		p.attach(c)
		return nil
	}
}

// Push splits pred into top-level conjuncts and pushes each to the deepest
// plan node covering its tables. Referencing a table absent from the plan
// is a decidable error.
func Push(p *Plan, pred *ast.Node) error {
	if p == nil || pred == nil {
		return fmt.Errorf("push: nil plan or predicate")
	}
	if err := ast.Validate(pred); err != nil {
		return err
	}
	have := p.Tables()
	conjs := []*ast.Node{pred}
	if pred.Kind == ast.KAnd {
		conjs = pred.Kids
	}
	for _, c := range conjs {
		tabs := ast.Tables(c)
		for t := range tabs {
			if !have[t] {
				return fmt.Errorf("conjunct %s references unknown table %s", c, t)
			}
		}
		if err := p.place(c, tabs); err != nil {
			return err
		}
	}
	return nil
}

// Check verifies every scan predicate references only its own table.
func Check(p *Plan) error {
	if p == nil {
		return fmt.Errorf("check: nil plan")
	}
	if p.Table != "" {
		if p.Pred == nil {
			return nil
		}
		for t := range ast.Tables(p.Pred) {
			if t != p.Table {
				return fmt.Errorf("scan %s predicate references foreign table %s", p.Table, t)
			}
		}
		return nil
	}
	if err := Check(p.Left); err != nil {
		return err
	}
	return Check(p.Right)
}

// String renders the plan with pushed predicates, deterministically.
func (p *Plan) String() string {
	if p.Table != "" {
		if p.Pred == nil {
			return "scan(" + p.Table + ")"
		}
		return "scan(" + p.Table + " | " + p.Pred.String() + ")"
	}
	var pred string
	if p.Pred != nil {
		pred = " | " + p.Pred.String()
	}
	kids := []string{p.Left.String(), p.Right.String()}
	sort.Strings(kids)
	return "join(" + strings.Join(kids, ", ") + pred + ")"
}
