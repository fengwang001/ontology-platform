// Package ontology implements instance storage with cross-type aggregate views.
package ontology

// ErrorCode is the normalized, mutually-distinguishable class of a rejected op.
type ErrorCode string

const (
	// ErrInvalidArgument: empty primary/group key or attribute type mismatch.
	ErrInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	// ErrVersionConflict: optimistic-concurrency credential mismatch,
	// including delete-again-after-delete.
	ErrVersionConflict ErrorCode = "VERSION_CONFLICT"
	// ErrNotFound: delete against a primary key that never committed a version.
	ErrNotFound ErrorCode = "NOT_FOUND"
)

// ConflictReason refines ErrVersionConflict without changing its class.
type ConflictReason string

const (
	ReasonWriteOnMissing ConflictReason = "WRITE_ON_MISSING" // expected>0, no live instance
	ReasonStaleVersion   ConflictReason = "STALE_VERSION"    // token != current version
	ReasonAlreadyDeleted ConflictReason = "ALREADY_DELETED"  // delete on tombstone
)

// OpError is the single normalized error shape emitted by the store.
type OpError struct {
	Code          ErrorCode
	Reason        ConflictReason
	Op            string
	TypeName      string
	Key           string
	GotVersion    int64
	WantedVersion int64
	Detail        string
}

func (e *OpError) Error() string {
	s := string(e.Code)
	if e.Reason != "" {
		s += "/" + string(e.Reason)
	}
	s += " " + e.Op + " " + e.TypeName + "/" + e.Key
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

func invalid(kind, typeName, key, detail string) *OpError {
	return &OpError{Code: ErrInvalidArgument, Op: kind, TypeName: typeName, Key: key, Detail: detail}
}

func conflict(kind, typeName, key string, reason ConflictReason, got, wanted int64, detail string) *OpError {
	return &OpError{Code: ErrVersionConflict, Reason: reason, Op: kind, TypeName: typeName,
		Key: key, GotVersion: got, WantedVersion: wanted, Detail: detail}
}

func notFound(kind, typeName, key, detail string) *OpError {
	return &OpError{Code: ErrNotFound, Op: kind, TypeName: typeName, Key: key, Detail: detail}
}
