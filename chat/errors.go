package chat

import (
	"errors"
	"fmt"
)

// Code identifies the rejection category of an operation. Rejections are
// reported in a fixed precedence order (invalid parameter > clock skew >
// not a member > message not found > permission denied > state); only the
// first matching category is reported and a rejected operation changes no
// state, sequence number or clock.
type Code int

const (
	// ErrInvalidParam: malformed arguments (empty ids, bad body, bad
	// mention set, out-of-range now/limit/seq, unknown mention target...).
	ErrInvalidParam Code = iota + 1
	// ErrClockSkew: now is smaller than the last accepted operation's now.
	ErrClockSkew
	// ErrNotMember: the calling user is not a current channel member.
	ErrNotMember
	// ErrMessageNotFound: seq does not identify an existing message.
	ErrMessageNotFound
	// ErrPermissionDenied: caller is neither the author nor an admin.
	ErrPermissionDenied
	// ErrAlreadyRecalled: the message has already been recalled.
	ErrAlreadyRecalled
	// ErrTimeout: the edit/recall window has elapsed (>= E / >= R).
	ErrTimeout
	// ErrEditLimit: the message already reached K edits.
	ErrEditLimit
	// ErrWatermarkRollback: MarkRead upto is below the current watermark.
	ErrWatermarkRollback
	// ErrOutOfRange: MarkRead upto exceeds the latest sequence number.
	ErrOutOfRange
)

var codeNames = map[Code]string{
	ErrInvalidParam:      "invalid parameter",
	ErrClockSkew:         "clock skew",
	ErrNotMember:         "not a member",
	ErrMessageNotFound:   "message not found",
	ErrPermissionDenied:  "permission denied",
	ErrAlreadyRecalled:   "already recalled",
	ErrTimeout:           "window expired",
	ErrEditLimit:         "edit limit exceeded",
	ErrWatermarkRollback: "watermark rollback",
	ErrOutOfRange:        "out of range",
}

func (c Code) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("code(%d)", int(c))
}

// Error is the single error type returned by all channel operations.
type Error struct {
	Op     string
	Code   Code
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("chat: %s rejected: %s (%s)", e.Op, e.Code, e.Detail)
}

// CodeOf extracts the rejection Code from an error returned by this package.
// It returns 0 for nil or foreign errors.
func CodeOf(err error) Code {
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return 0
}

func reject(op string, code Code, format string, args ...any) *Error {
	return &Error{Op: op, Code: code, Detail: fmt.Sprintf(format, args...)}
}
