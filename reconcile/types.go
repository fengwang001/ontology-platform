// Package reconcile merges snapshots produced independently by multiple
// replica nodes of the ontology platform into a single deterministic
// reconciled state.
//
// The package is split into three cooperating components:
//
//   - baseline   (baseline.go): determines the logical position baseline.
//   - quarantine (codec.go):    isolates structurally corrupted snapshots.
//   - arbiter    (arbiter.go):  adjudicates per-object value conflicts.
//
// Reconciler (reconciler.go) orchestrates the three. It is stateless and
// safe for concurrent use; it never mutates its inputs.
package reconcile

// Position is a logical position (monotonic sequence number) in a replica's
// history. Snapshots and individual property writes are tagged with it.
type Position uint64

// ReplicaID identifies a replica node.
type ReplicaID string

// ReplicaMeta carries the replica's self-described, comparable identity.
// Priority is the comparable identifier used by the fixed arbitration rule:
// a numerically smaller Priority wins. The rule is fixed and never changes
// based on participant count or arrival order.
type ReplicaMeta struct {
	ID       ReplicaID `json:"id"`
	Priority uint64    `json:"priority"`
}

// VersionedValue is a property value tagged with the logical position at
// which it was last written.
type VersionedValue struct {
	Value     string   `json:"value"`
	WrittenAt Position `json:"written_at"`
}

// ObjectInstance is the state of one object instance: property key -> value.
type ObjectInstance struct {
	Props map[string]VersionedValue `json:"props"`
}

// Snapshot is one replica's point-in-time view of object instances.
type Snapshot struct {
	Replica ReplicaMeta               `json:"replica"`
	Pos     Position                  `json:"position"`
	Objects map[string]ObjectInstance `json:"objects"`
}
