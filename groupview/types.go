package groupview

// Op identifies the kind of mutation applied to the maintained view.
type Op int

const (
	Insert Op = iota + 1
	Delete
)

// Mutation is a single insert or delete of one row.
type Mutation struct {
	Op    Op
	RowID string
	Group string
	Value int64
}

// Aggregate is the per-group aggregate that downstream stores.
type Aggregate struct {
	Count int64
	Sum   int64
}

// EntryKind distinguishes the retraction and upsert halves of an output entry.
type EntryKind int

const (
	Retract EntryKind = iota + 1
	Upsert
)

// Entry is one ordered output record: a retraction of an old aggregate
// followed by an upsert of the new aggregate.
type Entry struct {
	Group string
	Kind  EntryKind
	Value Aggregate
}

// RejectReason enumerates the distinguishable causes of a rejected batch.
type RejectReason int

const (
	ReasonEmptyGroup RejectReason = iota + 1
	ReasonDeleteMissing
	ReasonTooManyGroups
	ReasonDuplicateRow
)

// BatchError reports why a batch was rejected. A rejected batch never
// mutates aggregates, the downstream view, or the produced log.
type BatchError struct {
	Reason RejectReason
	Index  int
	Detail string
}

func (e *BatchError) Error() string {
	return e.Detail
}

// String renders the reason with a stable, distinguishable token so logs and
// tests can tell rejection causes apart.
func (r RejectReason) String() string {
	switch r {
	case ReasonEmptyGroup:
		return "EMPTY_GROUP"
	case ReasonDeleteMissing:
		return "DELETE_MISSING_ROW"
	case ReasonTooManyGroups:
		return "TOO_MANY_GROUPS"
	case ReasonDuplicateRow:
		return "DUPLICATE_ROW_ID"
	default:
		return "UNKNOWN"
	}
}
