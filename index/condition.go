package index

import "errors"

// Distinguishable rejection reasons for invalid queries. A rejected query
// is refused as a whole and never touches the scan statistics.
var (
	ErrUnknownColumn     = errors.New("index: unknown column")
	ErrTypeMismatch      = errors.New("index: value type does not match column type")
	ErrEmptySet          = errors.New("index: IN set must not be empty")
	ErrInvalidComboLimit = errors.New("index: prefix combination limit must be positive")
)

// Op is a comparison operator over one column.
type Op int

const (
	OpEq     Op = iota // =
	OpIn               // IN (set)
	OpLt               // <
	OpLe               // <=
	OpGt               // >
	OpGe               // >=
	OpIsNull           // IS NULL
)

func (op Op) String() string {
	switch op {
	case OpEq:
		return "="
	case OpIn:
		return "IN"
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	case OpIsNull:
		return "IS NULL"
	}
	return "?"
}

// Condition is a single predicate on one column. Value carries the operand
// for OpEq/OpLt/OpLe/OpGt/OpGe, Values for OpIn; OpIsNull takes none.
// Comparison operands must be non-NULL and match the column kind.
type Condition struct {
	Column string
	Op     Op
	Value  any
	Values []any
}

// colKind classifies the intersected conditions of one column.
type colKind int

const (
	colNone  colKind = iota // no condition on this column
	colEmpty                // contradictory conditions: matches nothing
	colEq                   // single point (incl. IS NULL and collapsed ranges)
	colSet                  // finite set of two or more values
	colRange                // lower/upper bounds
)

// colPlan is the normalized result of intersecting all conditions of one
// column.
type colPlan struct {
	kind     colKind
	eq       any   // colEq: the point value (nil means NULL)
	set      []any // colSet: sorted, deduplicated, len >= 2
	lower    any   // colRange: lower bound value
	lowerInc bool
	hasLower bool
	upper    any // colRange: upper bound value
	upperInc bool
	hasUpper bool
}

// colConstraint accumulates the intersection of conditions on one column.
type colConstraint struct {
	isNull   bool
	hasEq    bool
	eq       any
	hasSet   bool
	set      []any
	hasLower bool
	lower    any
	lowerInc bool
	hasUpper bool
	upper    any
	upperInc bool
	empty    bool
}

// intersectConditions validates and intersects all conditions per column.
// It returns one colPlan per column (indexed like cols).
func intersectConditions(cols []Column, conds []Condition) ([]colPlan, error) {
	colIdx := make(map[string]int, len(cols))
	for i, c := range cols {
		colIdx[c.Name] = i
	}
	cs := make([]colConstraint, len(cols))
	for _, cond := range conds {
		i, ok := colIdx[cond.Column]
		if !ok {
			return nil, errUnknownColumn(cond.Column)
		}
		if err := cs[i].add(cols[i].Kind, cond); err != nil {
			return nil, err
		}
	}
	plans := make([]colPlan, len(cols))
	for i := range cs {
		plans[i] = cs[i].normalize()
	}
	return plans, nil
}

func errUnknownColumn(name string) error {
	return &columnError{column: name}
}

type columnError struct{ column string }

func (e *columnError) Error() string { return ErrUnknownColumn.Error() + ": " + e.column }
func (e *columnError) Unwrap() error { return ErrUnknownColumn }

// add folds one condition into the constraint, validating its operands.
func (c *colConstraint) add(k Kind, cond Condition) error {
	if c.empty {
		return nil
	}
	norm := func(v any) (any, error) {
		if v == nil {
			return nil, errNullOperand(cond)
		}
		return normalizeValue(k, v)
	}
	switch cond.Op {
	case OpIsNull:
		c.isNull = true
	case OpEq:
		v, err := norm(cond.Value)
		if err != nil {
			return err
		}
		if c.hasEq && compareValues(c.eq, v) != 0 {
			c.empty = true
			return nil
		}
		c.hasEq, c.eq = true, v
	case OpIn:
		if len(cond.Values) == 0 {
			return errEmptySet(cond.Column)
		}
		vals := make([]any, 0, len(cond.Values))
		for _, raw := range cond.Values {
			v, err := norm(raw)
			if err != nil {
				return err
			}
			vals = append(vals, v)
		}
		vals = sortDedup(vals)
		if c.hasSet {
			vals = intersectSets(c.set, vals)
		}
		if len(vals) == 0 {
			c.empty = true
			return nil
		}
		c.hasSet, c.set = true, vals
	case OpLt, OpLe, OpGt, OpGe:
		v, err := norm(cond.Value)
		if err != nil {
			return err
		}
		switch cond.Op {
		case OpLt, OpLe:
			inc := cond.Op == OpLe
			if !c.hasUpper || compareValues(v, c.upper) < 0 ||
				(compareValues(v, c.upper) == 0 && !inc) {
				c.hasUpper, c.upper, c.upperInc = true, v, inc
			}
		case OpGt, OpGe:
			inc := cond.Op == OpGe
			if !c.hasLower || compareValues(v, c.lower) > 0 ||
				(compareValues(v, c.lower) == 0 && !inc) {
				c.hasLower, c.lower, c.lowerInc = true, v, inc
			}
		}
		if c.hasLower && c.hasUpper {
			cmp := compareValues(c.lower, c.upper)
			if cmp > 0 || (cmp == 0 && !(c.lowerInc && c.upperInc)) {
				c.empty = true
			}
		}
	}
	return nil
}

// normalize collapses the accumulated constraint into a colPlan.
func (c *colConstraint) normalize() colPlan {
	switch {
	case c.empty:
		return colPlan{kind: colEmpty}
	case c.isNull:
		// NULL never satisfies comparisons or set membership, so IS NULL
		// combined with any of them is contradictory; alone it is an
		// equality on NULL.
		if c.hasEq || c.hasSet || c.hasLower || c.hasUpper {
			return colPlan{kind: colEmpty}
		}
		return colPlan{kind: colEq, eq: nil}
	case c.hasEq:
		if c.hasSet && !inSet(c.set, c.eq) {
			return colPlan{kind: colEmpty}
		}
		if !withinBounds(c.eq, c) {
			return colPlan{kind: colEmpty}
		}
		return colPlan{kind: colEq, eq: c.eq}
	case c.hasSet:
		vals := make([]any, 0, len(c.set))
		for _, v := range c.set {
			if withinBounds(v, c) {
				vals = append(vals, v)
			}
		}
		switch len(vals) {
		case 0:
			return colPlan{kind: colEmpty}
		case 1:
			return colPlan{kind: colEq, eq: vals[0]}
		}
		return colPlan{kind: colSet, set: vals}
	case c.hasLower || c.hasUpper:
		if c.hasLower && c.hasUpper && compareValues(c.lower, c.upper) == 0 {
			// Both bounds inclusive (checked in add): a single point.
			return colPlan{kind: colEq, eq: c.lower}
		}
		return colPlan{
			kind:     colRange,
			lower:    c.lower,
			lowerInc: c.lowerInc,
			hasLower: c.hasLower,
			upper:    c.upper,
			upperInc: c.upperInc,
			hasUpper: c.hasUpper,
		}
	}
	return colPlan{kind: colNone}
}

func withinBounds(v any, c *colConstraint) bool {
	if c.hasLower {
		cmp := compareValues(v, c.lower)
		if cmp < 0 || (cmp == 0 && !c.lowerInc) {
			return false
		}
	}
	if c.hasUpper {
		cmp := compareValues(v, c.upper)
		if cmp > 0 || (cmp == 0 && !c.upperInc) {
			return false
		}
	}
	return true
}

func sortDedup(vals []any) []any {
	for i := 1; i < len(vals); i++ {
		for j := i; j > 0 && compareValues(vals[j-1], vals[j]) > 0; j-- {
			vals[j-1], vals[j] = vals[j], vals[j-1]
		}
	}
	out := vals[:0]
	for i, v := range vals {
		if i == 0 || compareValues(vals[i-1], v) != 0 {
			out = append(out, v)
		}
	}
	return out
}

func intersectSets(a, b []any) []any {
	out := make([]any, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch cmp := compareValues(a[i], b[j]); {
		case cmp == 0:
			out = append(out, a[i])
			i++
			j++
		case cmp < 0:
			i++
		default:
			j++
		}
	}
	return out
}

func inSet(set []any, v any) bool {
	for _, s := range set {
		if compareValues(s, v) == 0 {
			return true
		}
	}
	return false
}

func errEmptySet(col string) error {
	return &emptySetError{column: col}
}

type emptySetError struct{ column string }

func (e *emptySetError) Error() string { return ErrEmptySet.Error() + ": " + e.column }
func (e *emptySetError) Unwrap() error { return ErrEmptySet }

func errNullOperand(cond Condition) error {
	return &nullOperandError{column: cond.Column, op: cond.Op}
}

type nullOperandError struct {
	column string
	op     Op
}

func (e *nullOperandError) Error() string {
	return ErrTypeMismatch.Error() + ": NULL operand for " + e.op.String() + " on column " + e.column
}
func (e *nullOperandError) Unwrap() error { return ErrTypeMismatch }
