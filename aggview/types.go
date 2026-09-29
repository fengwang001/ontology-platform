// Package aggview implements an incrementally maintained grouped aggregation
// view with a visibility filter. Rows are inserted or deleted in batches;
// after every row change the view re-evaluates the owning group's visibility
// and emits a net change (retract/upsert) so that a downstream consumer which
// applies the log in order always holds the correct filtered view.
package aggview

import "errors"

// ChangeKind classifies what happened to a group's membership in the view as
// a result of a single row mutation.
type ChangeKind int

const (
	// KindNone: group was invisible before and remains invisible; no output.
	KindNone ChangeKind = iota
	// KindEnter: group became visible (retract nothing, upsert new value).
	KindEnter
	// KindLeave: group ceased to be visible (retract old value).
	KindLeave
	// KindChange: group stayed visible but its aggregate changed (retract old,
	// upsert new).
	KindChange
)

// String returns the stable textual form used in logs.
func (k ChangeKind) String() string {
	switch k {
	case KindEnter:
		return "enter"
	case KindLeave:
		return "leave"
	case KindChange:
		return "change"
	default:
		return "none"
	}
}

// OpKind identifies a mutation operation.
type OpKind int

const (
	// OpInsert adds (or replaces) a row.
	OpInsert OpKind = iota
	// OpDelete removes a row by id.
	OpDelete
)

// String returns the stable textual form used in logs.
func (o OpKind) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// A Row is the unit of input. Rows with an empty Group are rejected.
type Row struct {
	ID    string
	Group string
	Value int64
}

// A Mutation is a single row change within a batch.
type Mutation struct {
	Op  OpKind
	Row Row    // used by insert
	ID  string // used by delete
}

// Agg is the aggregate state of a group.
type Agg struct {
	Group string
	Count int64
	Sum   int64
}

// Visible reports whether the aggregate meets the filter (count >= MinCount
// and sum >= MinSum, both thresholds inclusive).
func (a Agg) Visible(minCount, minSum int64) bool {
	return a.Count >= minCount && a.Sum >= minSum
}

// EntryOp identifies the kind of output entry.
type EntryOp int

const (
	// EntryRetract withdraws a previously emitted aggregate value.
	EntryRetract EntryOp = iota
	// EntryUpsert writes (or replaces) the current aggregate value.
	EntryUpsert
)

// String returns the stable textual form used in logs.
func (e EntryOp) String() string {
	switch e {
	case EntryRetract:
		return "retract"
	case EntryUpsert:
		return "upsert"
	default:
		return "unknown"
	}
}

// LogEntry is one net-change record. A KindChange produces two entries
// (retract of the old value followed by upsert of the new value); KindEnter
// produces one upsert; KindLeave produces one retract; KindNone produces none.
type LogEntry struct {
	Seq   int64 // monotonically increasing within a view
	Kind  ChangeKind
	Op    EntryOp
	Group string
	Old   *Agg // nil when there is no old value
	New   *Agg // nil when there is no new value
}

// Rejection reasons. They are returned wrapped by Apply so callers can
// distinguish them with errors.Is.
var (
	// ErrEmptyGroup: a row references an empty group name.
	ErrEmptyGroup = errors.New("aggview: empty group name")
	// ErrUnknownOp: a mutation carries an unsupported operation.
	ErrUnknownOp = errors.New("aggview: unknown mutation operation")
	// ErrRowNotFound: a delete targets a row id that does not exist.
	ErrRowNotFound = errors.New("aggview: delete of non-existent row")
	// ErrGroupLimit: the batch would push the number of tracked groups over
	// the configured limit.
	ErrGroupLimit = errors.New("aggview: group count exceeds limit")
)
