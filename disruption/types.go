// Package disruption implements a PodDisruptionBudget eviction adjudicator
// for node-drain scenarios.
//
// The package is split into four cooperating layers:
//
//   - types.go: value types shared by every layer.
//   - errors.go: the ordered, programmatically inspectable error taxonomy.
//   - budget.go: selector matching and budget arithmetic (pure functions).
//   - state.go: the indexed cluster state (pods, budgets, membership,
//     in-flight evictions, deadline heap, monotonic clock, probe counters).
//   - service.go: adjudication rules: single evict, batch evict, confirm,
//     cancel, expiry reinstatement and all mutating cluster operations.
//   - naive.go: an independent O(N*M) reference implementation used by the
//     differential tests.
package disruption

// Phase is the lifecycle phase of a Pod.
type Phase int

const (
	// PhasePending mirrors Kubernetes Pending.
	PhasePending Phase = iota
	// PhaseRunning mirrors Kubernetes Running.
	PhaseRunning
	// PhaseSucceeded mirrors Kubernetes Succeeded.
	PhaseSucceeded
	// PhaseFailed mirrors Kubernetes Failed.
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case PhasePending:
		return "Pending"
	case PhaseRunning:
		return "Running"
	case PhaseSucceeded:
		return "Succeeded"
	case PhaseFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// terminal reports whether the phase is excluded from PDB accounting and
// from being adjudicated for eviction.
func (p Phase) terminal() bool { return p == PhaseSucceeded || p == PhaseFailed }

// PodID namespaces a pod name within a namespace; together they are unique.
type PodID struct {
	Namespace string
	Name      string
}

// Pod is the immutable-value view used as input. The store keeps its own
// mutable copy so callers can freely mutate the argument after the call.
type Pod struct {
	ID     PodID
	Labels map[string]string
	Phase  Phase
	Ready  bool
}

// BudgetID namespaces a budget name within a namespace.
type BudgetID struct {
	Namespace string
	Name      string
}

// IntOrPct is exactly one of:
//
//   - an absolute non-negative integer (Percent == false), or
//   - an integer percentage in the closed range [0,100] (Percent == true).
type IntOrPct struct {
	Value   int
	Percent bool
}

// Selector is an equality-based label selector: every listed key must be
// present on the pod with the given value. A nil/empty selector matches no
// pod (Kubernetes empty-selector semantics are intentionally NOT applied).
type Selector map[string]string

// PodDisruptionBudget specifies minAvailable OR maxUnavailable, never both.
type PodDisruptionBudget struct {
	ID           BudgetID
	Selector     Selector
	MinAvailable *IntOrPct
	MaxUnavail   *IntOrPct
}

// EvictionDecision reports the outcome of a single-pod adjudication.
type EvictionDecision struct {
	Allowed bool
}

// BudgetStatus is the always-derived view returned by BudgetQuota.
type BudgetStatus struct {
	Expected          int // matched, non-terminal pods
	CurrentReady      int // matched, non-terminal, ready and not evicting
	RequiredReady     int // pods that must remain ready
	DisruptionAllowed int // CurrentReady - RequiredReady, floored at zero
}

// Tick is a logical, monotonically non-decreasing timestamp injected by the
// caller. All accepted operations advance the clock; a regressing argument is
// rejected as ClockBacktrack.
type Tick int64
