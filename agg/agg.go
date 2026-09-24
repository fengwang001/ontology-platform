// Package agg implements the aggregate family. Each aggregate declares
// whether deleting a record requires the group's members (recompute).
package agg

// Agg is one incremental aggregate over a group's values.
type Agg interface {
	Name() string
	// Insert folds a new value in; always incremental.
	Insert(v float64)
	// Delete tries an incremental retract. It returns false when the
	// aggregate cannot retract from its own state and the view must
	// schedule a Recompute from the group's members.
	Delete(v float64) bool
	// NeedsMembersOnDelete declares the static retraction capability.
	NeedsMembersOnDelete() bool
	// Recompute rebuilds the state from member values.
	Recompute(members []float64)
	Value() float64
}

// Names lists all aggregates maintained per group.
func Names() []string { return []string{"count", "sum", "min", "max", "distinct"} }

// New builds one aggregate by name; unknown names return nil.
func New(name string) Agg {
	switch name {
	case "count":
		return &count{}
	case "sum":
		return &sumAgg{}
	case "min":
		return &minmax{isMin: true}
	case "max":
		return &minmax{}
	case "distinct":
		return &distinct{seen: map[float64]int{}}
	}
	return nil
}

type count struct{ n int64 }

func (a *count) Name() string                { return "count" }
func (a *count) Insert(float64)              { a.n++ }
func (a *count) Delete(float64) bool         { a.n--; return true }
func (a *count) NeedsMembersOnDelete() bool  { return false }
func (a *count) Recompute(m []float64)       { a.n = int64(len(m)) }
func (a *count) Value() float64              { return float64(a.n) }

type sumAgg struct{ s float64 }

func (a *sumAgg) Name() string               { return "sum" }
func (a *sumAgg) Insert(v float64)           { a.s += v }
func (a *sumAgg) Delete(v float64) bool      { a.s -= v; return true }
func (a *sumAgg) NeedsMembersOnDelete() bool { return false }
func (a *sumAgg) Recompute(m []float64) {
	a.s = 0
	for _, v := range m {
		a.s += v
	}
}
func (a *sumAgg) Value() float64 { return a.s }

// minmax keeps only the current extremum; retracting it is impossible
// without the members, so Delete fails exactly when v is the extremum.
type minmax struct {
	isMin bool
	v     float64
	set   bool
}

func (a *minmax) Name() string {
	if a.isMin {
		return "min"
	}
	return "max"
}
func (a *minmax) Insert(v float64) {
	if !a.set || (a.isMin && v < a.v) || (!a.isMin && v > a.v) {
		a.v, a.set = v, true
	}
}
func (a *minmax) Delete(v float64) bool     { return v != a.v }
func (a *minmax) NeedsMembersOnDelete() bool { return true }
func (a *minmax) Recompute(m []float64) {
	a.set = false
	for _, v := range m {
		a.Insert(v)
	}
}
func (a *minmax) Value() float64 { return a.v }

// distinct counts distinct values. Multiplicity is not derivable from the
// count alone, so every delete asks the view for a member recompute.
type distinct struct {
	seen map[float64]int
	n    int
}

func (a *distinct) Name() string { return "distinct" }
func (a *distinct) Insert(v float64) {
	a.seen[v]++
	if a.seen[v] == 1 {
		a.n++
	}
}
func (a *distinct) Delete(float64) bool      { return false }
func (a *distinct) NeedsMembersOnDelete() bool { return true }
func (a *distinct) Recompute(m []float64) {
	clear(a.seen)
	a.n = 0
	for _, v := range m {
		a.Insert(v)
	}
}
func (a *distinct) Value() float64 { return float64(a.n) }
