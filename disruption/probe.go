package disruption

// Probe counts primitive state touches so tests can prove operation cost
// independently of wall-clock timing:
//
//   - PodProbe: a single pod record touched;
//   - BudgetProbe: a single budget record touched;
//   - MemberProbe: a single membership entry touched;
//   - ExpiryProbe: one eviction deadline inspected via the heap.
//
// A correct adjudicator's single-evict / quota path increments only the
// budgets that could possibly match the pod (same namespace, selector
// candidate), never unrelated namespaces or unrelated budgets.
type Probe struct {
	PodProbe    int
	BudgetProbe int
	MemberProbe int
	ExpiryProbe int
}

// Snapshot returns a copy of the counters.
func (p Probe) Snapshot() Probe { return p }
