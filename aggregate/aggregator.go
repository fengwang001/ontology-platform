package aggregate

import (
	"sort"
	"sync"
)

// Result is one materialized output group. Key identifies the group; Sum is
// meaningful only when SumValid is true.
type Result struct {
	Key GroupKey
	// Count is the number of rows that fell into the group.
	Count int64
	// Skipped counts rows counted by Count but excluded from Sum: the sum
	// attribute was missing, nil, NaN, or held an unsummable type.
	Skipped int64
	// SumValid reports whether the group produced a sum. It is false when
	// no row contributed a summable value (all rows skipped, or empty group).
	SumValid bool
	// IsFloat reports whether Sum holds a floating-point result. It is true
	// when the contributing rows were all int64 that overflowed int64 is an
	// error rather than a float, so IsFloat is true only when at least one
	// contributing value was float64.
	IsFloat bool
	// IntSum is the exact int64 sum when SumValid && !IsFloat.
	IntSum int64
	// FloatSum is the result when SumValid && IsFloat, and is also where
	// int64-only sums are exposed after widening for mixed groups.
	FloatSum float64
	// Overflow is true when the group's all-int64 sum exceeded int64 range.
	// Such groups are returned from Snapshot with Overflow set and SumValid
	// false; Snapshot additionally reports them through an OverflowError.
	Overflow bool
}

// Sum returns the group's sum as a float64 together with whether a sum
// exists. Int64-only sums are widened exactly (all int64 are exactly
// representable as float64 only up to 2^53, so prefer IntSum when
// IsFloat is false).
func (r Result) Sum() (float64, bool) {
	switch {
	case !r.SumValid:
		return 0, false
	case r.IsFloat:
		return r.FloatSum, true
	default:
		return float64(r.IntSum), true
	}
}

type groupState struct {
	parts   []partID
	count   int64
	skipped int64
	// Sum storage. hasFloat is sticky: once any float64 contributes, all
	// int64 values accumulated so far (and later) are widened to floats.
	hasFloat bool
	intSum   int64
	ints     []int64 // valid only while !hasFloat
	floats   []float64
	overflow bool
}

// Aggregator groups rows by an ordered list of key attribute names and
// computes Count plus an order-independent Sum per group. It is safe for
// concurrent Add; Snapshot takes a consistent snapshot under the same mutex,
// so a concurrent Snapshot can never observe Count incremented without the
// matching sum contribution.
type Aggregator struct {
	keyAttrs []string
	sumAttr  string

	mu     sync.Mutex
	groups map[string]*groupState
}

// NewAggregator creates an aggregator grouping by keyAttrs (ordered,
// significant) and summing sumAttr.
func NewAggregator(keyAttrs []string, sumAttr string) *Aggregator {
	keys := append([]string(nil), keyAttrs...)
	return &Aggregator{
		keyAttrs: keys,
		sumAttr:  sumAttr,
		groups:   make(map[string]*groupState),
	}
}

// Add ingests one row. A row always lands in exactly one group (rows whose
// key columns are absent/null are placed in the corresponding distinguished
// missing groups, never dropped) and always contributes to that group's
// Count. It contributes to Sum only per classifySum.
func (a *Aggregator) Add(row map[string]any) {
	parts := make([]partID, len(a.keyAttrs))
	for i, name := range a.keyAttrs {
		parts[i] = classifyPart(name, row)
	}
	id := makeGroupID(parts)

	val, summable := classifySum(a.sumAttr, row)

	a.mu.Lock()
	defer a.mu.Unlock()

	g := a.groups[id.key]
	if g == nil {
		g = &groupState{parts: id.parts}
		a.groups[id.key] = g
	}
	g.count++
	if !summable {
		g.skipped++
		return
	}
	if g.overflow {
		return
	}
	if g.hasFloat || !val.isInt {
		if !g.hasFloat {
			// First float after a run of ints: widen every earlier int64
			// into the float bucket exactly once.
			for _, n := range g.ints {
				g.floats = append(g.floats, float64(n))
			}
			g.ints = nil
		}
		g.hasFloat = true
		if val.isInt {
			g.floats = append(g.floats, float64(val.i))
		} else {
			g.floats = append(g.floats, val.f)
		}
		return
	}
	// All-int group so far: accumulate with an explicit overflow check.
	sum, ok := addInt64(g.intSum, val.i)
	if !ok {
		g.overflow = true
		return
	}
	g.intSum = sum
	g.ints = append(g.ints, val.i)
}

// Snapshot returns groups in a stable order (see compareGroupID) with sums
// fully reduced at that instant. The returned slice and GroupKeys are
// independent copies: later Adds never mutate them. If one or more
// all-int64 groups overflowed, the returned results still include them
// (with Overflow set) and a non-nil *OverflowError naming every such group.
func (a *Aggregator) Snapshot() ([]Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	states := make([]*groupState, 0, len(a.groups))
	for _, g := range a.groups {
		states = append(states, g)
	}
	sort.Slice(states, func(i, j int) bool {
		return compareParts(states[i].parts, states[j].parts) < 0
	})

	results := make([]Result, 0, len(states))
	var overflowKeys []GroupKey
	for _, g := range states {
		r := Result{Count: g.count, Skipped: g.skipped}
		key := GroupKey{Columns: make([]KeyPart, len(a.keyAttrs))}
		for i, name := range a.keyAttrs {
			key.Columns[i] = idToPart(name, g.parts[i])
		}
		r.Key = key

		switch {
		case g.overflow:
			r.Overflow = true
			overflowKeys = append(overflowKeys, cloneKey(key))
		case g.hasFloat:
			floats := append([]float64(nil), g.floats...)
			r.SumValid = len(floats) > 0
			r.IsFloat = true
			r.FloatSum = orderIndependentSum(floats)
		default:
			r.SumValid = len(g.ints) > 0
			r.IntSum = g.intSum
		}
		results = append(results, r)
	}
	if len(overflowKeys) > 0 {
		return results, &OverflowError{Groups: overflowKeys}
	}
	return results, nil
}

func cloneKey(k GroupKey) GroupKey {
	cols := make([]KeyPart, len(k.Columns))
	copy(cols, k.Columns)
	return GroupKey{Columns: cols}
}
