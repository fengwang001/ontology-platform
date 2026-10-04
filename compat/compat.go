package compat

import (
	"sync/atomic"

	"ontology/schema"
)

type Reason int

const (
	// MissingNoDefault: the writer lacks the reader's field and the reader
	// cannot synthesize it (regardless of whether it is required).
	MissingNoDefault Reason = iota + 1
	// TypeMismatch: same-named writer field whose type cannot be promoted.
	TypeMismatch
	// OptionalToRequired: type promotes, but the writer field is optional
	// while the reader needs a required value with no default.
	OptionalToRequired
)

// Direction labels which mode-driven check failed during Publish.
type Direction int

const (
	Backward Direction = iota + 1
	Forward
)

// Violation identifies the first reader-side field that fails, in reader
// field order, and why.
type Violation struct {
	Field  string
	Reason Reason
}

// compared counts reader-side fields inspected by CanRead across the process.
var compared atomic.Uint64

// Compared returns the accumulated number of reader-side fields checked.
func Compared() uint64 { return compared.Load() }

// resetCompared zeroes the counter; used by the package's own tests.
func resetCompared() { compared.Store(0) }

// promotable reports whether a value written as from can be read as to.
// Only identity plus int32->int64, int32->float64, string->bytes are
// allowed; none of the reverse directions promote.
func promotable(from, to schema.Type) bool {
	if from == to {
		return true
	}
	switch to {
	case schema.Int64:
		return from == schema.Int32
	case schema.Float64:
		return from == schema.Int32
	case schema.Bytes:
		return from == schema.String
	}
	return false
}

// CanRead reports whether a reader using schema reader can consume data
// written by schema writer. Extra writer fields are ignored. The first
// offending reader field (in reader order) is returned.
func CanRead(reader, writer schema.View) (Violation, bool) {
	for _, rf := range reader.Fields() {
		compared.Add(1)
		wf, ok := writer.FieldByName(rf.Name)
		if !ok {
			if !rf.HasDefault {
				return Violation{Field: rf.Name, Reason: MissingNoDefault}, false
			}
			continue
		}
		if !promotable(wf.Type, rf.Type) {
			return Violation{Field: rf.Name, Reason: TypeMismatch}, false
		}
		if !wf.Required && rf.Required && !rf.HasDefault {
			return Violation{Field: rf.Name, Reason: OptionalToRequired}, false
		}
	}
	return Violation{}, true
}
