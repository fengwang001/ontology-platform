// Package agg defines the aggregator family: Count, Sum, Min, Max,
// DistinctCount. Each aggregator declares whether deleting a member requires
// access to the surviving members of its group.
package agg

// State is one aggregator's state for one group.
type State interface {
	// Add incorporates a member value.
	Add(v float64)
	// Remove retracts a member value; it is only called when the aggregator
	// can maintain itself without the member set.
	Remove(v float64)
	// Rebuild replaces the state from the full surviving member values.
	Rebuild(values []float64)
	// Value returns the current scalar aggregate.
	Value() float64
}

// Aggregator describes a family and manufactures per-group states.
type Aggregator interface {
	Name() string
	// NeedsMembersOnDelete reports whether a delete (or the removal half of
	// an update) requires the group's surviving members, i.e. whether view
	// must run a Recompute for groups touched by such changes.
	NeedsMembersOnDelete() bool
	New() State
	// Equal compares two reported values bitwise (used by audit).
	Equal(a, b float64) bool
}

// All returns the five aggregators in a fixed order.
func All() []Aggregator {
	return []Aggregator{Count{}, Sum{}, Min{}, Max{}, DistinctCount{}}
}
