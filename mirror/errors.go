// Package mirror implements a multi-member mirrored block volume with
// member failure handling, generation arbitration and partial resync.
package mirror

import "fmt"

// ErrorCode identifies the category of an operation failure. Codes are
// declared in reporting precedence order: when several error conditions
// hold simultaneously, only the one with the smallest code is reported.
type ErrorCode int

const (
	ErrCodeInvalidParam ErrorCode = iota + 1
	ErrCodeMemberNotFound
	ErrCodeStateMismatch
	ErrCodeGenerationAhead
	ErrCodeNonAuthoritative
	ErrCodeVolumeUnavailable
)

func (c ErrorCode) String() string {
	switch c {
	case ErrCodeInvalidParam:
		return "invalid parameter"
	case ErrCodeMemberNotFound:
		return "member not found"
	case ErrCodeStateMismatch:
		return "state mismatch"
	case ErrCodeGenerationAhead:
		return "generation ahead"
	case ErrCodeNonAuthoritative:
		return "non-authoritative member"
	case ErrCodeVolumeUnavailable:
		return "volume unavailable"
	}
	return "unknown error"
}

// Error is the single error type returned by all volume operations.
// Code is always one of the ErrCode* values and allows callers to
// distinguish failure categories without string matching.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mirror: %s: %s", e.Code, e.Msg)
}

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
