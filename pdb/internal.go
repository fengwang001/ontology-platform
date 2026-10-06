package pdb

// podEntry is the stored state of one pod.
type podEntry struct {
	pod      Pod
	evicting *eviction
	// Index memberships are maintained via service-level maps keyed by labels.
}

// budgetEntry is the stored state of one budget.
type budgetEntry struct {
	spec     BudgetSpec
	expected int // matched in-stats pods
	ready    int // matched in-stats pods currently ready
	// memberships are derived through the service label indexes.
}

// deadlineHeap is a min-heap of evictions ordered by deadline.
type deadlineHeap []*eviction

// stats counters used by the verifiable complexity test.
type costStats struct {
	PodScans    int64
	BudgetScans int64
}
