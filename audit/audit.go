// Package audit cross-checks an incrementally maintained view against a full
// recomputation from the surviving base-table members.
package audit

import (
	"fmt"
	"math"

	"ontology/change"
	"ontology/view"
)

// Result is the fully recomputed snapshot of one group.
type Result struct {
	Count, Sum, Min, Max, Distinct float64
}

type full struct {
	count    int64
	sum      float64
	min, max float64
	values   map[uint64]int
}

func newFull() *full {
	return &full{values: map[uint64]int{}}
}

// Recompute derives every group aggregate directly from the member tables.
func Recompute(members map[string]map[uint64]float64) map[string]Result {
	out := make(map[string]Result, len(members))
	for g, ms := range members {
		f := newFull()
		for _, v := range ms {
			f.count++
			f.sum += v
			if f.count == 1 {
				f.min, f.max = v, v
			}
			if nz(v) < nz(f.min) {
				f.min = v
			}
			if nz(v) > nz(f.max) {
				f.max = v
			}
			f.values[math.Float64bits(nz(v))]++
		}
		if f.count == 0 {
			continue
		}
		out[g] = Result{
			Count: float64(f.count), Sum: f.sum, Min: f.min,
			Max: f.max, Distinct: float64(len(f.values)),
		}
	}
	return out
}

// Members rebuilds the surviving member tables from the change sequence.
func Members(recs []change.Change) map[string]map[uint64]float64 {
	groupOf := map[uint64]string{}
	valOf := map[uint64]float64{}
	for _, c := range recs {
		switch c.Op {
		case change.Insert:
			groupOf[c.RecID], valOf[c.RecID] = c.Group, c.Value
		case change.Delete:
			delete(groupOf, c.RecID)
			delete(valOf, c.RecID)
		case change.Update:
			groupOf[c.RecID], valOf[c.RecID] = c.NewGroup, c.NewValue
		}
	}
	members := map[string]map[uint64]float64{}
	for id, g := range groupOf {
		if members[g] == nil {
			members[g] = map[uint64]float64{}
		}
		members[g][id] = valOf[id]
	}
	return members
}

// Diff returns the first mismatching group (bitwise for Sum), if any.
func Diff(full map[string]Result, got map[string]view.Group) (string, error) {
	if len(full) != len(got) {
		return "", fmt.Errorf("audit: group count %d != %d", len(got), len(full))
	}
	for g, want := range full {
		have, ok := got[g]
		if !ok {
			return g, fmt.Errorf("audit: group %q missing", g)
		}
		if math.Float64bits(want.Sum) != math.Float64bits(have.Sum) {
			return g, fmt.Errorf("audit: %q Sum bits %x != %x", g,
				math.Float64bits(have.Sum), math.Float64bits(want.Sum))
		}
		if want.Count != have.Count || want.Min != have.Min ||
			want.Max != have.Max || want.Distinct != have.Distinct {
			return g, fmt.Errorf("audit: %q mismatch %+v != %+v", g, have, want)
		}
	}
	return "", nil
}

func nz(v float64) float64 {
	if math.Float64bits(v) == 1<<63 {
		return 0
	}
	return v
}
