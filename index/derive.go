package index

import (
	"fmt"
	"strings"
)

// Bound is one end of a key interval. Prefix holds the leading column
// values the bound pins down; it may be shorter than the number of columns
// (an empty prefix denotes the open end of the whole index). A key K
// satisfies a lower bound when cmpPrefix(K, Prefix) > 0, or == 0 when
// Inclusive; it satisfies an upper bound when cmpPrefix(K, Prefix) < 0, or
// == 0 when Inclusive.
type Bound struct {
	Prefix    []any
	Inclusive bool
}

func (b Bound) String() string {
	if len(b.Prefix) == 0 {
		if b.Inclusive {
			return "∞"
		}
		return "∅"
	}
	parts := make([]string, len(b.Prefix))
	for i, v := range b.Prefix {
		parts[i] = formatValue(v)
	}
	s := "(" + strings.Join(parts, ", ") + ")"
	if !b.Inclusive {
		s += " exclusive"
	}
	return s
}

// Interval is a contiguous range of keys in index order.
type Interval struct {
	Lower Bound
	Upper Bound
}

func (iv Interval) String() string {
	return "[" + iv.Lower.String() + ", " + iv.Upper.String() + "]"
}

// contains reports whether a full key falls inside the interval.
func (iv Interval) contains(key []any) bool {
	if c := cmpPrefix(key, iv.Lower.Prefix); c < 0 || (c == 0 && !iv.Lower.Inclusive) {
		return false
	}
	if c := cmpPrefix(key, iv.Upper.Prefix); c > 0 || (c == 0 && !iv.Upper.Inclusive) {
		return false
	}
	return true
}

// Plan is the derived access path: an ordered, non-overlapping set of key
// intervals plus the residual conditions applied to every examined entry.
type Plan struct {
	Intervals []Interval
	// PrefixCols is the number of leading columns pinned by the intervals.
	PrefixCols int
	// RangeCol is the index of the column merged into the interval bounds,
	// or -1 when the prefix ends without a range column.
	RangeCol int
	// ResidualFrom is the first column whose conditions are only applied
	// as residual filters (degraded columns included).
	ResidualFrom int
	// Empty is true when contradictory conditions yield no intervals.
	Empty bool
}

func (p *Plan) String() string {
	if p.Empty {
		return "empty plan (contradictory conditions): 0 intervals"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d interval(s), prefixCols=%d, rangeCol=%d, residualFrom=%d:",
		len(p.Intervals), p.PrefixCols, p.RangeCol, p.ResidualFrom)
	for _, iv := range p.Intervals {
		fmt.Fprintf(&sb, "\n  %s", iv)
	}
	return sb.String()
}

// derivePlan validates the query and compiles it into a Plan, also
// returning the conditions with normalized operand values for residual
// evaluation. comboLimit is the maximum number of prefix combinations (L);
// when the combinations of consecutive equality/set columns would exceed
// it, the column that first crosses the limit and everything after it
// degrade to residual filters.
func derivePlan(cols []Column, conds []Condition, comboLimit int) (*Plan, []Condition, error) {
	if comboLimit <= 0 {
		return nil, nil, fmt.Errorf("%w, got %d", ErrInvalidComboLimit, comboLimit)
	}
	plans, err := intersectConditions(cols, conds)
	if err != nil {
		return nil, nil, err
	}
	norm := normalizeConditions(cols, conds)
	for _, p := range plans {
		if p.kind == colEmpty {
			return &Plan{Empty: true, RangeCol: -1}, norm, nil
		}
	}
	// No condition on the first column: the whole index is one interval.
	if plans[0].kind == colNone {
		return &Plan{
			Intervals:    []Interval{fullInterval()},
			RangeCol:     -1,
			ResidualFrom: 0,
		}, norm, nil
	}

	// Walk the leading equality/set columns, then optionally merge the
	// first range column into the bounds and stop.
	sets := make([][]any, 0, len(cols)) // per prefix column: candidate values
	combos := 1
	stop := -1 // first column not part of the prefix
	for i, p := range plans {
		switch p.kind {
		case colEq:
			sets = append(sets, []any{p.eq})
		case colSet:
			if combos*len(p.set) > comboLimit {
				// Combination limit exceeded: degrade from this column on.
				stop = i
			} else {
				combos *= len(p.set)
				sets = append(sets, p.set)
			}
		default:
			stop = i
		}
		if stop >= 0 {
			break
		}
	}
	if stop < 0 {
		stop = len(plans)
	}

	plan := &Plan{PrefixCols: len(sets), RangeCol: -1, ResidualFrom: stop}
	var rangeLower, rangeUpper Bound
	hasLower, hasUpper := false, false
	if stop < len(plans) && plans[stop].kind == colRange {
		r := plans[stop]
		plan.RangeCol = stop
		plan.ResidualFrom = stop + 1
		if r.hasLower {
			hasLower = true
			rangeLower = Bound{Prefix: []any{r.lower}, Inclusive: r.lowerInc}
		}
		if r.hasUpper {
			hasUpper = true
			rangeUpper = Bound{Prefix: []any{r.upper}, Inclusive: r.upperInc}
		}
	}

	// Cartesian product of the prefix columns, generated in index
	// (lexicographic) order so intervals come out sorted and disjoint.
	for _, combo := range cartesian(sets) {
		lower := Bound{Prefix: combo, Inclusive: true}
		upper := Bound{Prefix: combo, Inclusive: true}
		if hasLower {
			lower = Bound{Prefix: append(append([]any{}, combo...), rangeLower.Prefix...), Inclusive: rangeLower.Inclusive}
		}
		if hasUpper {
			upper = Bound{Prefix: append(append([]any{}, combo...), rangeUpper.Prefix...), Inclusive: rangeUpper.Inclusive}
		}
		plan.Intervals = append(plan.Intervals, Interval{Lower: lower, Upper: upper})
	}
	return plan, norm, nil
}

// normalizeConditions rewrites every condition with canonical operand
// values (int64/float64/string). Callers must have validated the
// conditions first (via intersectConditions).
func normalizeConditions(cols []Column, conds []Condition) []Condition {
	colIdx := make(map[string]int, len(cols))
	for i, c := range cols {
		colIdx[c.Name] = i
	}
	out := make([]Condition, len(conds))
	for i, cond := range conds {
		k := cols[colIdx[cond.Column]].Kind
		nc := cond
		if cond.Op == OpIn {
			nc.Values = make([]any, len(cond.Values))
			for j, v := range cond.Values {
				nc.Values[j], _ = normalizeValue(k, v)
			}
		} else if cond.Op != OpIsNull {
			nc.Value, _ = normalizeValue(k, cond.Value)
		}
		out[i] = nc
	}
	return out
}

func fullInterval() Interval {
	return Interval{
		Lower: Bound{Prefix: nil, Inclusive: true},
		Upper: Bound{Prefix: nil, Inclusive: true},
	}
}

// cartesian yields the combinations of the per-column value lists in
// lexicographic order (last column varies fastest).
func cartesian(sets [][]any) [][]any {
	if len(sets) == 0 {
		return [][]any{{}}
	}
	first := make([]any, len(sets))
	for j, s := range sets {
		first[j] = s[0]
	}
	out := [][]any{first}
	idx := make([]int, len(sets))
	for {
		// Advance the odometer.
		i := len(sets) - 1
		for ; i >= 0; i-- {
			if idx[i]+1 < len(sets[i]) {
				idx[i]++
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return out
		}
		combo := make([]any, len(sets))
		for j, s := range sets {
			combo[j] = s[idx[j]]
		}
		out = append(out, combo)
	}
}

func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
