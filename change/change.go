// Package change defines a single base-table change flowing through the
// incremental view maintainer.
//
// A change is one of Insert, Delete or Update. Every change carries a
// monotonically increasing Version assigned by the producer. Rows are
// identified by Key; the view keeps one member entry per live key so that
// aggregates can be recomputed from group membership when required.
package change

import (
	"errors"
	"math"
)

// Op identifies the kind of base-table mutation.
type Op uint8

const (
	OpUnknown Op = 0
	OpInsert  Op = 1
	OpDelete  Op = 2
	OpUpdate  Op = 3
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpDelete:
		return "delete"
	case OpUpdate:
		return "update"
	default:
		return "unknown"
	}
}

// ErrMalformed is returned when an encoded change cannot be decoded.
var ErrMalformed = errors.New("change: malformed record")

// Row is the group/value coordinate of one base-table record.
// GroupPresent distinguishes a legitimate empty-string group key from a
// record that carries no group key at all (which the view must reject).
type Row struct {
	Group        string
	GroupPresent bool
	Value        float64
}

// Change is one versioned base-table mutation.
//
// For Insert and Delete only From is meaningful (the inserted/removed row).
// For Update From is the old row and To is the new row, so an update may
// move a record between groups.
type Change struct {
	Version uint64
	Op      Op
	Key     string
	From    Row
	To      Row
}

// Rows returns the rows affected by the change, old then new.
func (c Change) Rows() (from, to Row) {
	switch c.Op {
	case OpUpdate:
		return c.From, c.To
	default:
		return c.From, Row{}
	}
}

// NormZero canonicalizes -0 to +0. The boundary semantics require +0 and
// -0 to be treated as equal; normalizing on ingest keeps float64 map keys
// and every aggregate bit-stable. NaN is passed through unchanged (the
// view rejects NaN before it can reach aggregation).
func NormZero(x float64) float64 {
	if x == 0 {
		return math.Copysign(0, 1)
	}
	return x
}
