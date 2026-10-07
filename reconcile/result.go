package reconcile

// IssueKind classifies the three distinct, fixed-priority error categories.
type IssueKind string

const (
	// KindInsufficientReplicas: not enough readable replicas to determine
	// the position baseline. Fatal; checked first.
	KindInsufficientReplicas IssueKind = "insufficient_replicas"
	// KindCorruptedSnapshot: a replica snapshot is structurally unreadable
	// and was quarantined. Non-fatal; evaluated second.
	KindCorruptedSnapshot IssueKind = "corrupted_snapshot"
	// KindIrreconcilable: an object instance still has conflicting values
	// after the fixed arbitration rule was applied (the comparable
	// identifiers themselves tie). Evaluated last, per object.
	KindIrreconcilable IssueKind = "irreconcilable"
)

// QuarantineRecord reports one corrupted replica snapshot excluded from
// content arbitration. Records are identified by content (replica id and
// failure reason), never by arrival order, so results do not drift when
// the same inputs arrive in a different order.
type QuarantineRecord struct {
	Kind    IssueKind `json:"kind"`
	Replica ReplicaID `json:"replica,omitempty"`
	Reason  string    `json:"reason"`
}

// Candidate describes one replica's offered value for a conflicting
// property, retained for auditability.
type Candidate struct {
	Replica  ReplicaID `json:"replica"`
	Priority uint64    `json:"priority"`
	Value    string    `json:"value"`
}

// ConflictRecord is the decision rationale for one object instance whose
// properties were in conflict.
type ConflictRecord struct {
	ObjectID string              `json:"object_id"`
	Resolved map[string]Decision `json:"resolved,omitempty"`
	// Unresolved is non-empty exactly when the object is irreconcilable
	// (KindIrreconcilable).
	Unresolved []PropertyConflict `json:"unresolved,omitempty"`
}

// Decision explains how one conflicting property was resolved.
type Decision struct {
	Prop      string      `json:"prop"`
	Winner    Candidate   `json:"winner"`
	Losers    []Candidate `json:"losers"`
	Rationale string      `json:"rationale"`
}

// PropertyConflict describes a property whose candidates tie under the
// fixed arbitration rule.
type PropertyConflict struct {
	Prop       string      `json:"prop"`
	Candidates []Candidate `json:"candidates"`
	Reason     string      `json:"reason"`
}

// Stats exposes operation counters so the cost of a reconciliation run can
// be audited: total work is linear in the number of input values, and
// arbitration work is proportional to the number of real conflicts only.
type Stats struct {
	ReplicasInput       int `json:"replicas_input"`
	ReplicasQuarantine  int `json:"replicas_quarantined"`
	ReplicasUsed        int `json:"replicas_used"`
	ValuesScanned       int `json:"values_scanned"`
	ValuesFiltered      int `json:"values_filtered"` // excluded: written after baseline
	PropsMerged         int `json:"props_merged"`
	ConflictsFound      int `json:"conflicts_found"`
	ArbitrationsRun     int `json:"arbitrations_run"` // always == ConflictsFound
	PropsIrreconcilable int `json:"props_irreconcilable"`
}

// Result is the deterministic output of one reconciliation run.
type Result struct {
	// Baseline is the earliest logical position among participating
	// replicas; the reconciled state corresponds to this position.
	Baseline Position `json:"baseline"`
	// Objects is the reconciled content: object ID -> property -> value.
	Objects map[string]map[string]string `json:"objects"`
	// Quarantined lists corrupted replicas excluded from arbitration.
	Quarantined []QuarantineRecord `json:"quarantined,omitempty"`
	// Conflicts records every conflict decision, resolved or not.
	Conflicts []ConflictRecord `json:"conflicts,omitempty"`
	// Irreconcilable lists object IDs excluded from Objects because at
	// least one property could not be uniquely resolved.
	Irreconcilable []string `json:"irreconcilable,omitempty"`
	Stats          Stats    `json:"stats"`
}
