package ontology

// JournalRecord is the complete, deterministic evidence trail of one batch
// attempt. Every attempt (committed or rejected) is recorded; the same
// BatchInput replayed on an identically seeded store yields identical
// records.
type JournalRecord struct {
	// Seq is the dense attempt sequence assigned by the store.
	Seq int64

	// Input is the full batch request as supplied by the caller.
	Input BatchInput

	// Committed reports the final verdict.
	Committed bool

	// Failure is nil for committed batches and otherwise carries the single
	// mutually exclusive rejection cause.
	Failure *BatchError

	// Phase states which ordered check produced the verdict:
	// "duplicate", "version", "validation", "cardinality" or "commit".
	Phase string

	// InstanceTouches counts, per instance, how many stored instance states
	// were read during decision making. It is the evidence that the decision
	// cost depends only on the batch's own working set and never on the total
	// number of instances in the store.
	InstanceTouches map[InstanceID]int

	// LockOrder is the sorted instance set over which locks were acquired.
	LockOrder []InstanceID

	// Baselines is a copy of the declared-vs-observed versions checked.
	Baselines []BaselineObservation

	// Hooks is one entry per hook executed, in execution order.
	Hooks []HookObservation

	// CardinalityChecks lists every endpoint degree evaluated against the
	// post-batch image.
	CardinalityChecks []CardinalityObservation

	// FinalVersions is populated for committed batches and maps every written
	// instance to its new version.
	FinalVersions map[InstanceID]Version
}

// BaselineObservation records one per-instance optimistic-version check.
type BaselineObservation struct {
	ID       InstanceID
	Declared Version
	Observed Version
	Match    bool
}

// HookObservation records one validation-hook invocation.
type HookObservation struct {
	Instance InstanceID
	Type     ObjectTypeName
	Rejected bool
	Reason   string
}

// CardinalityObservation records one endpoint cardinality evaluation on the
// final image of the batch.
type CardinalityObservation struct {
	Link      LinkTypeName
	Endpoint  InstanceID
	Side      string // "source" or "target"
	Degree    int
	Bound     Cardinality
	Satisfied bool
}
