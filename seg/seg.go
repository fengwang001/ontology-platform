// Package seg defines a single MVCC record, its validation, version
// comparison and tombstone test. It depends on no other package.
package seg

import "errors"

// Rec is one MVCC record. Version is globally strictly increasing.
type Rec struct {
	Key     string
	Version int64
	Op      string
	Val     string
}

// Op values.
const (
	OpPut = "put"
	OpDel = "del"
)

// Distinct, decidable sentinel errors for malformed records.
var (
	// ErrEmptyKey: Key is the empty string.
	ErrEmptyKey = errors.New("seg: empty key")
	// ErrBadOp: Op is neither put nor del.
	ErrBadOp = errors.New("seg: invalid op")
	// ErrBadVal: put has empty Val, or del has non-empty Val.
	ErrBadVal = errors.New("seg: invalid val for op")
)

// Valid reports whether the record is well formed apart from global
// version monotonicity, which the api package enforces. Checks are
// ordered key, op, val so the four failure classes stay distinguishable:
// empty key, illegal op, val/op mismatch, and (in api) bad version.
func (r Rec) Valid() error {
	if r.Key == "" {
		return ErrEmptyKey
	}
	switch r.Op {
	case OpPut:
		if r.Val == "" {
			return ErrBadVal
		}
	case OpDel:
		if r.Val != "" {
			return ErrBadVal
		}
	default:
		return ErrBadOp
	}
	return nil
}

// IsTombstone reports whether r is a deletion.
func (r Rec) IsTombstone() bool { return r.Op == OpDel }

// Newer reports whether version v is strictly greater than than.
func Newer(v, than int64) bool { return v > than }
