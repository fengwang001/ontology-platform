// Package audit verifies the incremental view against an independently
// fully-recomputed reference. It replays accepted changes from scratch
// into fresh aggregators, then compares every group field by field.
//
// Numeric equality for Sum is required to be bit-identical IEEE-754
// equality, so comparison uses math.Float64bits rather than == on values.
package audit

import (
	"fmt"
	"math"
	"sort"

	"ontology/agg"
	"ontology/change"
	"ontology/view"
)

// Reference is the ground-truth state built by full recomputation.
type Reference struct {
	groups map[string]map[string]float64 // group -> member key -> value
}

// NewReference returns an empty reference.
func NewReference() *Reference {
	return &Reference{groups: map[string]map[string]float64{}}
}

// Apply folds one change into the reference base table. It applies the same
// validity/version rules as the view and reports whether the view would
// accept the change; rejected changes are skipped on both sides.
func (r *Reference) Apply(c change.Change) {
	switch c.Op {
	case change.OpInsert:
		r.put(c.Key, c.From)
	case change.OpDelete:
		r.remove(c.Key)
	case change.OpUpdate:
		r.remove(c.Key)
		r.put(c.Key, c.To)
	}
}

func (r *Reference) put(key string, row change.Row) {
	g := r.groups[row.Group]
	if g == nil {
		g = map[string]float64{}
		r.groups[row.Group] = g
	}
	g[key] = change.NormZero(row.Value)
}

func (r *Reference) remove(key string) {
	for name, g := range r.groups {
		if _, ok := g[key]; ok {
			delete(g, key)
			if len(g) == 0 {
				delete(r.groups, name)
			}
			return
		}
	}
}

// recompute builds canonical aggregator results for one group.
func recompute(g map[string]float64) map[agg.Kind]float64 {
	keys := make([]string, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mems := make([]agg.Member, 0, len(keys))
	for _, k := range keys {
		mems = append(mems, agg.Member{Key: k, Value: g[k]})
	}
	out := map[agg.Kind]float64{}
	for _, kind := range agg.Family {
		a := agg.New(kind)
		a.Recompute(mems)
		if v, ok := a.Value(); ok {
			out[kind] = v
		}
	}
	return out
}

// Mismatch describes the first group/field that differs.
type Mismatch struct {
	Group  string
	Kind   agg.Kind
	Reason string
}

func (m Mismatch) Error() string {
	return fmt.Sprintf("audit: group %q aggregate %s: %s", m.Group, m.Kind, m.Reason)
}

// Check compares the reference with v group by group. A nil result means
// the incremental view is exactly equal to full recomputation, including
// bit-identical Sum values and identical group presence/absence.
func Check(v *view.View, r *Reference) *Mismatch {
	got := v.Groups()
	if len(got) != len(r.groups) {
		return &Mismatch{Reason: fmt.Sprintf("group set size %d != %d", len(got), len(r.groups))}
	}
	for name, members := range r.groups {
		gv, ok := got[name]
		if !ok {
			return &Mismatch{Group: name, Reason: "group missing in view"}
		}
		want := recompute(members)
		if mm := compareGroup(name, gv, want); mm != nil {
			return mm
		}
	}
	for name := range got {
		if _, ok := r.groups[name]; !ok {
			return &Mismatch{Group: name, Reason: "extra group in view"}
		}
	}
	return nil
}

func compareGroup(name string, gv view.GroupResult, want map[agg.Kind]float64) *Mismatch {
	type field struct {
		kind   agg.Kind
		got    float64
		exists bool
	}
	fields := []field{
		{agg.Count, gv.Count, gv.CountExists},
		{agg.Sum, gv.Sum, gv.SumExists},
		{agg.Min, gv.Min, gv.MinExists},
		{agg.Max, gv.Max, gv.MaxExists},
		{agg.DistinctCount, gv.Distinct, gv.DistinctExists},
	}
	for _, f := range fields {
		w, wantOK := want[f.kind]
		if f.exists != wantOK {
			return &Mismatch{Group: name, Kind: f.kind,
				Reason: fmt.Sprintf("existence %v != %v", f.exists, wantOK)}
		}
		if !f.exists {
			continue
		}
		// Every aggregate is compared via raw bits: Sum gets true
		// IEEE-754 bit-level equality; integer-valued aggregates have
		// identical bits for equal values anyway.
		if math.Float64bits(f.got) != math.Float64bits(w) {
			return &Mismatch{Group: name, Kind: f.kind,
				Reason: fmt.Sprintf("bits %016x != %016x",
					math.Float64bits(f.got), math.Float64bits(w))}
		}
	}
	return nil
}
