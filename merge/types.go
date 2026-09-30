// Package merge implements in-batch merging of partial-column update events.
//
// A batch of change events (inserts / updates) against the same primary key is
// collapsed into at most one output change per key, such that applying the
// merged result to the pre-batch table is equivalent to applying the original
// events one by one, and is reproducible.
package merge

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Tri-state column value
// ---------------------------------------------------------------------------

// Value is a tri-state column value.
//
// A column value is exactly one of:
//   - Absent:        the column is not present at all (Present == false).
//   - Explicit null: the column is present and explicitly null (Present && IsNull).
//   - String:        the column is present and holds a string (Present && !IsNull),
//     which may be the empty string "".
//
// Absent, explicit-null and the empty string are three DISTINCT states and are
// never treated as equal to one another.
type Value struct {
	Present bool   // false => Absent
	IsNull  bool   // Present && IsNull => explicit null
	Str     string // Present && !IsNull => string value ("" is a real value)
}

// Absent returns the absent (missing) value.
func Absent() Value { return Value{} }

// Null returns the explicit-null value.
func Null() Value { return Value{Present: true, IsNull: true} }

// Str returns a string value ("" is a valid, distinct value).
func Str(s string) Value { return Value{Present: true, Str: s} }

// IsAbsent reports whether the value is absent.
func (v Value) IsAbsent() bool { return !v.Present }

// IsNullValue reports whether the value is an explicit null.
func (v Value) IsNullValue() bool { return v.Present && v.IsNull }

// IsString reports whether the value is a (possibly empty) string.
func (v Value) IsString() bool { return v.Present && !v.IsNull }

// Equal reports tri-state equality: absent==absent, null==null, and string
// equality by exact bytes. Absent != null != "" (all distinct).
func (v Value) Equal(o Value) bool {
	if v.Present != o.Present {
		return false
	}
	if !v.Present {
		return true // both absent
	}
	if v.IsNull != o.IsNull {
		return false
	}
	if v.IsNull {
		return true // both explicit null
	}
	return v.Str == o.Str
}

// String renders the value for logs: <absent>, <null>, or the quoted string.
func (v Value) String() string {
	switch {
	case !v.Present:
		return "<absent>"
	case v.IsNull:
		return "<null>"
	default:
		return fmt.Sprintf("%q", v.Str)
	}
}

// ---------------------------------------------------------------------------
// Row / ColumnSet
// ---------------------------------------------------------------------------

// Row maps a column name to its tri-state value. A column that is absent is
// simply not a key in the map (or maps to an Absent value).
type Row map[string]Value

// Clone returns a deep copy of the row.
func (r Row) Clone() Row {
	if r == nil {
		return nil
	}
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// ColumnSet is a set of column names.
type ColumnSet map[string]struct{}

// NewColumnSet builds a set from names.
func NewColumnSet(names ...string) ColumnSet {
	s := make(ColumnSet, len(names))
	for _, n := range names {
		s[n] = struct{}{}
	}
	return s
}

// Has reports whether the set contains the column.
func (s ColumnSet) Has(col string) bool {
	_, ok := s[col]
	return ok
}

// Sorted returns the column names in deterministic (sorted) order.
func (s ColumnSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// EqualSet reports whether two sets contain exactly the same columns.
func (s ColumnSet) EqualSet(o ColumnSet) bool {
	if len(s) != len(o) {
		return false
	}
	for c := range s {
		if !o.Has(c) {
			return false
		}
	}
	return true
}

// ColumnsOf returns the set of column names present in a row.
func ColumnsOf(r Row) ColumnSet {
	s := make(ColumnSet, len(r))
	for c := range r {
		s[c] = struct{}{}
	}
	return s
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// EventKind is the kind of a change event.
type EventKind int

const (
	// EventInsert inserts a brand-new row; it must provide every column.
	EventInsert EventKind = iota
	// EventUpdate updates a subset of columns of an existing row.
	EventUpdate
)

func (k EventKind) String() string {
	switch k {
	case EventInsert:
		return "insert"
	case EventUpdate:
		return "update"
	default:
		return "unknown"
	}
}

// Event is a single change event in a batch.
//
// Insert: Key must not exist; Columns must contain exactly the schema columns
// (all present, none absent).
//
// Update: Key must exist; Columns holds only the columns changed by this
// event; Before is the pre-image and must cover exactly the same column set as
// Columns, and (for the first in-batch update of a key) must match the actual
// stored values.
type Event struct {
	Kind    EventKind
	Key     string
	Columns Row // changed/provided columns (tri-state values)
	Before  Row // update only: pre-image, same column set as Columns
}

// ---------------------------------------------------------------------------
// Merged output
// ---------------------------------------------------------------------------

// Change is the merged output for a single key.
type Change struct {
	Key     string
	Kind    EventKind // EventInsert or EventUpdate
	Columns Row       // insert: full row; update: surviving changed columns
	Before  Row       // update only: merged pre-image (first in-batch value per column)
}

// Result is the outcome of merging a batch.
//
// Changes contains at most one Change per key, ordered by the key's first
// appearance in the batch. Keys whose merged update became empty (every change
// equals the original value) are omitted.
type Result struct {
	Changes []Change
}

// Keys returns the output keys in first-appearance order.
func (r *Result) Keys() []string {
	out := make([]string, 0, len(r.Changes))
	for _, c := range r.Changes {
		out = append(out, c.Key)
	}
	return out
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrorKind classifies a batch rejection. Each kind is distinct so callers can
// tell rejections apart programmatically.
type ErrorKind int

const (
	// ErrIllegalColumn: unknown column, insert missing/extra columns, update
	// with no columns, or before-image column set != changed column set.
	ErrIllegalColumn ErrorKind = iota
	// ErrKeyNotFound: an update targets a key that does not exist.
	ErrKeyNotFound
	// ErrKeyExists: an insert targets a key that already exists.
	ErrKeyExists
	// ErrBeforeImageMismatch: a before-image value does not match the actual
	// value at the moment the event is applied.
	ErrBeforeImageMismatch
	// ErrConflict: a concurrent commit changed the table since the batch was
	// merged (optimistic-concurrency failure on commit).
	ErrConflict
)

func (k ErrorKind) String() string {
	switch k {
	case ErrIllegalColumn:
		return "illegal-column"
	case ErrKeyNotFound:
		return "key-not-found"
	case ErrKeyExists:
		return "key-exists"
	case ErrBeforeImageMismatch:
		return "before-image-mismatch"
	case ErrConflict:
		return "commit-conflict"
	default:
		return "unknown-error"
	}
}

// BatchError describes why a batch was rejected. It is the only error type
// returned by Merge and Commit.
type BatchError struct {
	Kind    ErrorKind
	Key     string // offending key (may be empty if not key-specific)
	Column  string // offending column (may be empty)
	EventIx int    // index of the offending event in the batch (-1 if n/a)
	Detail  string // human-readable explanation
}

func (e *BatchError) Error() string {
	var b strings.Builder
	b.WriteString("batch rejected: ")
	b.WriteString(e.Kind.String())
	if e.Key != "" {
		fmt.Fprintf(&b, " key=%q", e.Key)
	}
	if e.Column != "" {
		fmt.Fprintf(&b, " column=%q", e.Column)
	}
	if e.EventIx >= 0 {
		fmt.Fprintf(&b, " event=%d", e.EventIx)
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Logger
// ---------------------------------------------------------------------------

// Logger receives human-readable trace lines.
type Logger interface {
	Logf(format string, args ...any)
}

// LoggerFunc adapts a function to Logger.
type LoggerFunc func(format string, args ...any)

// Logf implements Logger.
func (f LoggerFunc) Logf(format string, args ...any) { f(format, args...) }

// noopLogger is the default.
type noopLogger struct{}

func (noopLogger) Logf(string, ...any) {}
