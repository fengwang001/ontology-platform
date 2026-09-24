// Package filter prunes rows and predicates according to a role policy.
// A predicate referencing any column invisible to the role is rejected
// outright (design option 丙), except references eliminated by constant
// folding (TRUE OR x, FALSE AND x). Checking is one post-order traversal.
package filter

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"ontology/policy"
	"ontology/predicate"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvisibleColumn  = errors.New("predicate references invisible column")
	ErrMissingColumn    = errors.New("referenced column missing from row")
	ErrInvalidPredicate = errors.New("invalid predicate")
)

// Rejection reports every surviving invisible-column reference.
type Rejection struct {
	Refs []predicate.Ref
}

func (e *Rejection) Error() string {
	parts := make([]string, len(e.Refs))
	for i, r := range e.Refs {
		parts[i] = r.Column + " at " + r.Path
	}
	return ErrInvisibleColumn.Error() + ": " + strings.Join(parts, "; ")
}

// Is lets errors.Is(err, ErrInvisibleColumn) match a Rejection.
func (e *Rejection) Is(target error) bool { return target == ErrInvisibleColumn }

// Checker validates predicates against a policy in one traversal.
type Checker struct {
	pol     policy.Policy
	visited int
	cols    []string
}

// NewChecker returns a Checker enforcing pol.
func NewChecker(pol policy.Policy) *Checker { return &Checker{pol: pol} }

// Visited returns how many predicate nodes the last Check visited.
func (c *Checker) Visited() int { return c.visited }

// Columns returns the sorted columns the folded predicate still reads.
func (c *Checker) Columns() []string { return c.cols }

// Check folds constants and collects invisible references in a single
// post-order traversal. A nil root means "no filtering" and is allowed.
func (c *Checker) Check(root *predicate.Node) error {
	c.visited = 0
	c.cols = nil
	if root == nil {
		return nil
	}
	_, _, refs, cols, err := c.walk(root, "$")
	if err != nil {
		return err
	}
	sort.Strings(cols)
	c.cols = slices.Compact(cols)
	if len(refs) > 0 {
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Path != refs[j].Path {
				return refs[i].Path < refs[j].Path
			}
			return refs[i].Column < refs[j].Column
		})
		return &Rejection{Refs: refs}
	}
	return nil
}

// walk returns (isConst, constVal, invisibleRefs, survivingCols, err).
func (c *Checker) walk(n *predicate.Node, path string) (bool, bool, []predicate.Ref, []string, error) {
	if n == nil {
		return false, false, nil, nil, fmt.Errorf("%w: nil node at %s", ErrInvalidPredicate, path)
	}
	c.visited++
	switch n.Kind {
	case predicate.Const:
		return true, n.Bool, nil, nil, nil
	case predicate.Compare:
		if n.Column == "" || !validOp(n.Op) {
			return false, false, nil, nil, fmt.Errorf("%w: bad compare at %s", ErrInvalidPredicate, path)
		}
		if !c.pol.Visible(n.Column) {
			return false, false, []predicate.Ref{{Column: n.Column, Path: path}}, []string{n.Column}, nil
		}
		return false, false, nil, []string{n.Column}, nil
	case predicate.Not:
		if len(n.Children) != 1 {
			return false, false, nil, nil, fmt.Errorf("%w: NOT arity at %s", ErrInvalidPredicate, path)
		}
		kc, kv, refs, cols, err := c.walk(n.Children[0], path+"/NOT")
		if err != nil || kc {
			return kc, !kv, nil, nil, err
		}
		return false, false, refs, cols, nil
	case predicate.And, predicate.Or:
		if len(n.Children) == 0 {
			return false, false, nil, nil, fmt.Errorf("%w: empty junction at %s", ErrInvalidPredicate, path)
		}
		name := "AND"
		short := false // AND short-circuits on constant FALSE
		if n.Kind == predicate.Or {
			name, short = "OR", true // OR short-circuits on constant TRUE
		}
		var refs []predicate.Ref
		var cols []string
		alive := false
		for i, kid := range n.Children {
			kc, kv, kr, kcols, err := c.walk(kid, fmt.Sprintf("%s/%s[%d]", path, name, i))
			if err != nil {
				return false, false, nil, nil, err
			}
			if kc && kv == short { // whole junction folds to the constant
				return true, short, nil, nil, nil
			}
			if !kc {
				alive = true
				refs = append(refs, kr...)
				cols = append(cols, kcols...)
			}
		}
		if !alive {
			return true, !short, nil, nil, nil
		}
		return false, false, refs, cols, nil
	}
	return false, false, nil, nil, fmt.Errorf("%w: kind %d at %s", ErrInvalidPredicate, n.Kind, path)
}

func validOp(op predicate.Op) bool {
	switch op {
	case predicate.Eq, predicate.Ne, predicate.Lt, predicate.Gt,
		predicate.Le, predicate.Ge, predicate.IsNull, predicate.IsNotNul:
		return true
	}
	return false
}

// PruneRow copies only the visible columns of row into a fresh map and
// reports how many key/value pairs were copied. Cost is proportional to
// the number of visible columns, independent of the row's total width.
func PruneRow(row map[string]any, pol policy.Policy) (map[string]any, int) {
	out := make(map[string]any, len(pol.Columns()))
	copies := 0
	for _, col := range pol.Columns() {
		if v, ok := row[col]; ok {
			out[col] = v
			copies++
		}
	}
	return out, copies
}

// Query checks pred, evaluates it over rows, and prunes the survivors.
// On rejection it returns the offending references with the error.
func Query(rows []map[string]any, pred *predicate.Node, pol policy.Policy) ([]map[string]any, []predicate.Ref, error) {
	chk := NewChecker(pol)
	if err := chk.Check(pred); err != nil {
		var rej *Rejection
		if errors.As(err, &rej) {
			return nil, rej.Refs, err
		}
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		for _, col := range chk.Columns() {
			if _, ok := row[col]; !ok {
				return nil, nil, fmt.Errorf("%w: %q", ErrMissingColumn, col)
			}
		}
		if pred != nil {
			keep, err := predicate.Eval(pred, row)
			if err != nil {
				return nil, nil, err
			}
			if !keep {
				continue
			}
		}
		pruned, _ := PruneRow(row, pol)
		out = append(out, pruned)
	}
	return out, nil, nil
}
