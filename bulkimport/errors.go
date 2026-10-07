package bulkimport

import "fmt"

// ErrorKind classifies import errors. The numeric value is the fixed
// reporting priority: lower wins when several conditions hold at once.
//
// Required priority order (highest first):
//  1. ErrKindChunkConflict      - duplicate sequence, different content
//  2. ErrKindRefFailed          - referenced entry deterministically failed
//  3. ErrKindDanglingTimeout    - chunk-count limit reached while dangling
//  4. ErrKindValidation         - the entry itself failed validation
//  5. ErrKindJobClosed          - chunk arrived after the job was closed
//
// ErrKindChunkLimitExceeded is an additional kind (more chunks than the
// declared limit); it ranks below all of the above.
type ErrorKind int

const (
	ErrKindChunkConflict ErrorKind = iota + 1
	ErrKindRefFailed
	ErrKindDanglingTimeout
	ErrKindValidation
	ErrKindJobClosed
	ErrKindChunkLimitExceeded
)

// Priority returns the reporting priority; lower is reported first.
func (k ErrorKind) Priority() int { return int(k) }

func (k ErrorKind) String() string {
	switch k {
	case ErrKindChunkConflict:
		return "chunk_conflict"
	case ErrKindRefFailed:
		return "ref_failed"
	case ErrKindDanglingTimeout:
		return "dangling_timeout"
	case ErrKindValidation:
		return "validation"
	case ErrKindJobClosed:
		return "job_closed"
	case ErrKindChunkLimitExceeded:
		return "chunk_limit_exceeded"
	default:
		return "invalid"
	}
}

// ImportError is a classified, queryable import failure.
type ImportError struct {
	Kind ErrorKind
	// EntryID is the entry this error is attached to, when applicable.
	EntryID string
	// RefID is the referenced entry that caused a ErrKindRefFailed
	// propagation, when applicable.
	RefID   string
	Message string
}

func (e *ImportError) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.RefID != "":
		return fmt.Sprintf("%s: entry %q: %s (ref %q)", e.Kind, e.EntryID, e.Message, e.RefID)
	case e.EntryID != "":
		return fmt.Sprintf("%s: entry %q: %s", e.Kind, e.EntryID, e.Message)
	default:
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
}
