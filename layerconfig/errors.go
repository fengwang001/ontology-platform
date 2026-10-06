// Package layerconfig implements a versioned, four-layered configuration
// system with explicit cancellation, per-key merge policies, layer locks,
// atomic publication, historical reads and rollback.
package layerconfig

import "fmt"

// ErrorKind identifies the class of an error. The kinds are ordered by
// priority (smaller rank = higher priority); when an operation could be
// rejected for more than one reason, the highest-priority kind wins.
type ErrorKind int

const (
	// KindInvalidArgument: malformed parameters (priority 1, highest).
	KindInvalidArgument ErrorKind = iota + 1
	// KindVersionNotFound: the referenced historical version does not exist.
	KindVersionNotFound
	// KindKeyNotRegistered: a change or read references an unregistered key.
	KindKeyNotRegistered
	// KindTypeOrRange: value type/range violates the key schema.
	KindTypeOrRange
	// KindLockConflict: a narrower layer writes under an effective lock, or a
	// new lock is introduced while narrower writes already exist.
	KindLockConflict
	// KindBatchConflict: the same publication contains conflicting changes to
	// one (layer, key).
	KindBatchConflict
	// KindRequiredMissing: after publication, some required key is unset in an
	// existing environment/region/instance scope.
	KindRequiredMissing
)

// String returns a stable human-readable name of the error kind.
func (k ErrorKind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindVersionNotFound:
		return "VersionNotFound"
	case KindKeyNotRegistered:
		return "KeyNotRegistered"
	case KindTypeOrRange:
		return "TypeOrRange"
	case KindLockConflict:
		return "LockConflict"
	case KindBatchConflict:
		return "BatchConflict"
	case KindRequiredMissing:
		return "RequiredMissing"
	default:
		return fmt.Sprintf("ErrorKind(%d)", int(k))
	}
}

// Priority returns the numeric priority of the kind: smaller means higher
// priority and reported first when several problems apply.
func (k ErrorKind) Priority() int { return int(k) }

// Error is the single error type returned by this package. Inspect Kind to
// branch on error category; Detail carries a human-readable explanation.
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Detail }

func errInvalid(format string, args ...any) error {
	return &Error{Kind: KindInvalidArgument, Detail: fmt.Sprintf(format, args...)}
}

func errVersion(format string, args ...any) error {
	return &Error{Kind: KindVersionNotFound, Detail: fmt.Sprintf(format, args...)}
}

func errKey(key string) error {
	return &Error{Kind: KindKeyNotRegistered, Detail: "key not registered: " + key}
}

func errType(format string, args ...any) error {
	return &Error{Kind: KindTypeOrRange, Detail: fmt.Sprintf(format, args...)}
}

func errLock(format string, args ...any) error {
	return &Error{Kind: KindLockConflict, Detail: fmt.Sprintf(format, args...)}
}

func errBatch(format string, args ...any) error {
	return &Error{Kind: KindBatchConflict, Detail: fmt.Sprintf(format, args...)}
}

func errRequired(format string, args ...any) error {
	return &Error{Kind: KindRequiredMissing, Detail: fmt.Sprintf(format, args...)}
}

// AsError extracts *Error from err, if present.
func AsError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}
