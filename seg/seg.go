// Package seg defines a single versioned record and its validation.
// It depends on nothing else in the module.
package seg

import "errors"

// Op kinds; the only two legal values of Record.Op.
const (
	OpPut = "put"
	OpDel = "del"
)

// Sentinel errors, one per illegal-record category.
var (
	ErrKey     = errors.New("seg: empty key")
	ErrVersion = errors.New("seg: version not positive")
	ErrOp      = errors.New("seg: op must be put or del")
	ErrVal     = errors.New("seg: put needs non-empty val, del needs empty val")
)

// Record is one MVCC entry. Version is globally and strictly increasing.
type Record struct {
	Key     string
	Version int64
	Op      string
	Val     string
}

// Tombstone reports whether the record is a deletion marker.
func (r Record) Tombstone() bool { return r.Op == OpDel }

// Newer reports whether r carries a strictly greater version than o.
func (r Record) Newer(o Record) bool { return r.Version > o.Version }

// Validate checks everything about a record except cross-record version
// monotonicity (which only the ingest path can judge).
func (r Record) Validate() error {
	if r.Key == "" {
		return ErrKey
	}
	if r.Version <= 0 {
		return ErrVersion
	}
	if r.Op != OpPut && r.Op != OpDel {
		return ErrOp
	}
	if r.Op == OpPut && r.Val == "" {
		return ErrVal
	}
	if r.Op == OpDel && r.Val != "" {
		return ErrVal
	}
	return nil
}
