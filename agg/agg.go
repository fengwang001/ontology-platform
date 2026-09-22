// Package agg defines the aggregator family used by the incremental view.
//
// Each aggregator answers two contract questions up front:
//
//   - CanInsert: whether Insert can be folded into maintained state.
//   - NeedsMembersOnDelete: whether Delete needs access to the surviving
//     members of the group. When true, the view must run its Recompute
//     phase for that group instead of applying an inverse delta.
//
// Aggregators additionally expose a canonical Recompute over the exact
// surviving member multiset. The view uses it only when an incremental
// deletion is impossible; audit uses it for every group as ground truth.
package agg

// Member is one surviving record of a group, identified by its base-table
// key. Values may repeat across distinct keys.
type Member struct {
	Key   string
	Value float64
}

// Kind names a supported aggregator.
type Kind string

const (
	Count         Kind = "count"
	Sum           Kind = "sum"
	Min           Kind = "min"
	Max           Kind = "max"
	DistinctCount Kind = "distinct_count"
)

// Aggregator is one maintained aggregate.
type Aggregator interface {
	Kind() Kind
	// Insert folds one new member into maintained state.
	Insert(m Member)
	// DeleteIncremental tries to remove a member in place. ok is false
	// when the aggregate cannot be derived from maintained state alone;
	// the caller must then use Recompute with the surviving members.
	DeleteIncremental(m Member) (ok bool)
	// NeedsMembersOnDelete declares the static withdrawal strategy.
	NeedsMembersOnDelete() bool
	// Recompute builds the value canonically from the full surviving
	// multiset. It must be called with an empty state.
	Recompute(members []Member)
	// Reset empties all maintained state.
	Reset()
	// Value returns the scalar result, valid only when Exists is true.
	Value() (v float64, exists bool)
}

// Family is the ordered set of aggregators maintained for every group.
var Family = []Kind{Count, Sum, Min, Max, DistinctCount}

// New constructs a fresh aggregator of the requested kind.
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countAgg{}
	case Sum:
		return newSumAgg()
	case Min:
		return newMinAgg()
	case Max:
		return newMaxAgg()
	case DistinctCount:
		return &distinctAgg{}
	default:
		panic("agg: unknown kind " + k)
	}
}
