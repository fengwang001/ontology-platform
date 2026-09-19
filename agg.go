package ontology

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Aggregator groups rows by an ordered list of attributes and computes
// Count and Sum per group. Safe for concurrent use: a single mutex
// makes each Add atomic, so a concurrent Snapshot never observes a
// half-updated group (Count incremented but Sum not, or vice versa).
type Aggregator struct {
	mu      sync.Mutex
	keys    []string
	sumAttr string
	groups  map[string]*group
}

// group is the mutable per-group state.
type group struct {
	parts []KeyPart
	count int64
	sum   *groupSum
}

// NewAggregator creates an Aggregator grouping by groupKeys (ordered)
// and summing the attribute sumAttr.
func NewAggregator(groupKeys []string, sumAttr string) *Aggregator {
	keys := make([]string, len(groupKeys))
	copy(keys, groupKeys)
	return &Aggregator{
		keys:    keys,
		sumAttr: sumAttr,
		groups:  make(map[string]*group),
	}
}

// Add feeds one row into the aggregator. Rows with missing key columns
// are grouped under the corresponding missing kind, never dropped.
func (a *Aggregator) Add(row map[string]any) {
	parts := keyPartsFromRow(row, a.keys)
	id := encodeParts(parts)
	sumVal, sumPresent := row[a.sumAttr]

	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.groups[id]
	if !ok {
		g = &group{parts: parts, sum: newGroupSum()}
		a.groups[id] = g
	}
	g.count++
	g.sum.addValue(sumVal, sumPresent)
}

// GroupResult is the immutable result of one group.
type GroupResult struct {
	Key      []KeyPart
	Count    int64
	Skipped  int64
	IsInt    bool
	IntSum   int64
	FloatSum float64
}

// Sum returns the group's sum as a float64, converting the all-int64
// case. Callers that need the exact integer should check IsInt first.
func (r GroupResult) Sum() float64 {
	if r.IsInt {
		return float64(r.IntSum)
	}
	return r.FloatSum
}

// OverflowError is returned by Snapshot when one or more groups summed
// only int64 values whose exact total exceeds the int64 range. It is
// decidable with errors.As and identifies every offending group.
type OverflowError struct {
	Groups [][]KeyPart
}

func (e *OverflowError) Error() string {
	rendered := make([]string, len(e.Groups))
	for i, g := range e.Groups {
		rendered[i] = KeyString(g)
	}
	return fmt.Sprintf("ontology: int64 sum overflow in group(s): %s",
		strings.Join(rendered, ", "))
}

// KeyString renders a group key unambiguously, e.g. "(us, <nil>)".
func KeyString(parts []KeyPart) string {
	rendered := make([]string, len(parts))
	for i, p := range parts {
		rendered[i] = p.String()
	}
	return "(" + strings.Join(rendered, ", ") + ")"
}

// Snapshot returns the current results as an independent copy, sorted
// deterministically by key column (absent < nil < empty < values, then
// lexicographically by canonical value token). Repeating Snapshot on
// the same input always yields an element-wise identical sequence,
// regardless of map iteration order. If any all-int64 group overflowed,
// the non-overflowing results are still returned alongside an
// *OverflowError naming the offending group(s).
func (a *Aggregator) Snapshot() ([]GroupResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	results := make([]GroupResult, 0, len(a.groups))
	var overflowed [][]KeyPart
	for _, g := range a.groups {
		res := g.sum.result()
		if res.overflow {
			key := make([]KeyPart, len(g.parts))
			copy(key, g.parts)
			overflowed = append(overflowed, key)
			continue
		}
		key := make([]KeyPart, len(g.parts))
		copy(key, g.parts)
		results = append(results, GroupResult{
			Key:      key,
			Count:    g.count,
			Skipped:  g.sum.skipped,
			IsInt:    res.isInt,
			IntSum:   res.intSum,
			FloatSum: res.floatSum,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return compareParts(results[i].Key, results[j].Key) < 0
	})
	if len(overflowed) > 0 {
		sort.Slice(overflowed, func(i, j int) bool {
			return compareParts(overflowed[i], overflowed[j]) < 0
		})
		return results, &OverflowError{Groups: overflowed}
	}
	return results, nil
}
