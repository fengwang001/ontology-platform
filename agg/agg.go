// Package agg defines the aggregator family and, for each aggregator,
// whether deletion can be maintained incrementally or must fall back to
// recomputation from the group's members.
package agg

// Kind identifies one aggregator of the family.
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

func (k Kind) String() string {
	switch k {
	case Count:
		return "Count"
	case Sum:
		return "Sum"
	case Min:
		return "Min"
	case Max:
		return "Max"
	case DistinctCount:
		return "DistinctCount"
	}
	return "unknown"
}

// All lists the whole aggregator family in stable order.
func All() []Kind {
	return []Kind{Count, Sum, Min, Max, DistinctCount}
}

// IncrementalOnInsert reports whether a newly inserted value can be folded
// into the aggregate state without looking at the group's members.
// Every aggregator of this family can.
func (k Kind) IncrementalOnInsert() bool { return true }

// NeedsMembersOnDelete declares whether deleting a record may require the
// group's members to restore a correct aggregate. Count and Sum are
// exactly invertible (subtract the deleted contribution); Min, Max and
// DistinctCount are not, because the scalar state does not retain the
// information needed to derive the new aggregate.
func (k Kind) NeedsMembersOnDelete() bool {
	switch k {
	case Min, Max, DistinctCount:
		return true
	default:
		return false
	}
}

// RecomputeOnDelete decides, for a concrete deletion, whether the
// aggregate must be recomputed from members. Min/Max only need it when
// the deleted value equals the current extremum; DistinctCount always
// does (the state does not track per-value multiplicities).
func (k Kind) RecomputeOnDelete(deleted, current float64) bool {
	switch k {
	case Min, Max:
		return deleted == current
	case DistinctCount:
		return true
	default:
		return false
	}
}

// Compute evaluates the aggregate over a full member multiset. It is the
// single source of truth shared by the view's Recompute phase and the
// auditor's full recomputation.
func (k Kind) Compute(members []float64) float64 {
	switch k {
	case Count:
		return float64(len(members))
	case Sum:
		var s float64
		for _, v := range members {
			s += v
		}
		return s
	case Min:
		m := members[0]
		for _, v := range members[1:] {
			if v < m {
				m = v
			}
		}
		return m
	case Max:
		m := members[0]
		for _, v := range members[1:] {
			if v > m {
				m = v
			}
		}
		return m
	case DistinctCount:
		seen := make(map[float64]struct{}, len(members))
		for _, v := range members {
			seen[v] = struct{}{}
		}
		return float64(len(seen))
	}
	return 0
}
