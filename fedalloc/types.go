// Package fedalloc implements a multi-cluster workload replica federation
// allocator: minimum guarantees, weight-proportional sharing with largest
// remainder tie-breaking, cap saturation with redistribution, unavailable
// cluster evacuation and all-or-nothing rejection.
package fedalloc

// Cluster describes a registered cluster.
type Cluster struct {
	// Name is the unique cluster name.
	Name string
	// Weight is a non-negative integer scheduling weight.
	Weight int64
	// MinReplicas is the guaranteed minimum number of replicas.
	MinReplicas int64
	// MaxReplicas optionally bounds the replica count; nil means unbounded.
	MaxReplicas *int64
	// Capacity is the schedulable capacity (hard upper bound).
	Capacity int64
	// Available reports whether the cluster participates in an allocation.
	Available bool
	// CurrentReplicas is the number of replicas currently hosted.
	CurrentReplicas int64
}

// Target is the resulting target replica count for a single cluster.
type Target struct {
	// Name is the cluster name.
	Name string
	// Replicas is the computed target replica count.
	Replicas int64
}

// Change is a per-cluster delta relative to the currently hosted replicas.
// Clusters whose target equals their current load are omitted from a plan.
type Change struct {
	// Name is the cluster name.
	Name string
	// Current is the previously hosted replica count.
	Current int64
	// Target is the desired replica count.
	Target int64
	// Delta is Target - Current (positive for growth, negative for shrink).
	Delta int64
}

// AllocationResult is the output of a successful allocation.
type AllocationResult struct {
	// Total is the exact total number of replicas requested.
	Total int64
	// Targets holds one entry per cluster of the snapshot, even unavailable ones.
	Targets []Target
	// Plan holds only clusters whose target differs from their current load.
	Plan []Change
	// TotalMigration is the sum of absolute negative deltas.
	TotalMigration int64
}

// ErrorKind classifies a rejected request.
type ErrorKind int

const (
	// KindInvalidArgument: malformed or illegal input. Highest priority.
	KindInvalidArgument ErrorKind = iota
	// KindConfigConflict: a minimum guarantee exceeds the effective cap.
	KindConfigConflict
	// KindMinExceedsTotal: the sum of minimums exceeds the requested total.
	KindMinExceedsTotal
	// KindInsufficientCapacity: all clusters saturated while replicas remain.
	KindInsufficientCapacity
)

// AllocationError carries a classified, prioritised rejection reason.
type AllocationError struct {
	kind    ErrorKind
	message string
}

func (e *AllocationError) Error() string { return e.message }

// Kind returns the error category.
func (e *AllocationError) Kind() ErrorKind { return e.kind }

func newError(kind ErrorKind, message string) *AllocationError {
	return &AllocationError{kind: kind, message: message}
}
