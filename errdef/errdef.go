// Package errdef defines the typed error taxonomy for Ontology.
package errdef

import (
	"errors"
	"fmt"
)

// Code is the stable type identifier of an error. It survives serialization.
type Code string

const (
	CodeUnknown          Code = "unknown"
	CodeNotFound         Code = "not_found"
	CodeAlreadyExists    Code = "already_exists"
	CodeConflict         Code = "conflict"
	CodeValidation       Code = "validation"
	CodePermissionDenied Code = "permission_denied"
	CodeInternal         Code = "internal"
)

// isCompareCount counts typed comparisons performed by Error.Is. It proves that
// matching cost depends on the number of typed comparisons, not on unrelated
// wrapper layers: wrapping 100 or 10000 times still yields a single comparison.
var isCompareCount int

// IsCompareCount returns the current typed-comparison counter value.
func IsCompareCount() int { return isCompareCount }

// ResetCompareCount zeroes the typed-comparison counter.
func ResetCompareCount() { isCompareCount = 0 }

// Error is the single concrete error type. Type hierarchy is expressed through
// Is: every concrete error Is-A ErrBase, and matches the sentinel of its code.
type Error struct {
	code     Code
	rawCode  string // original string when reconstructed from an unknown code
	msg      string
	object   string
	property string
	cause    error
}

// Option configures an Error at construction time.
type Option func(*Error)

// WithObject attaches the affected object name.
func WithObject(object string) Option {
	return func(e *Error) { e.object = object }
}

// WithProperty attaches the affected property name.
func WithProperty(property string) Option {
	return func(e *Error) { e.property = property }
}

// WithCause attaches an underlying cause.
func WithCause(cause error) Option {
	return func(e *Error) { e.cause = cause }
}

// New builds a typed error. An empty code is normalized to CodeUnknown.
func New(code Code, msg string, opts ...Option) *Error {
	if code == "" {
		code = CodeUnknown
	}
	e := &Error{code: code, msg: msg}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// NewUnknown rebuilds an error carrying a code the current build does not know.
// The raw code is preserved verbatim instead of panicking.
func NewUnknown(rawCode, msg string, opts ...Option) *Error {
	e := New(CodeUnknown, msg, opts...)
	e.rawCode = rawCode
	return e
}

// ErrBase is the abstract base class: every Ontology error Is-An ErrBase.
var ErrBase = New(CodeUnknown, "ontology error")

// Concrete sentinels, one per known code.
var (
	ErrNotFound         = New(CodeNotFound, "not found")
	ErrAlreadyExists    = New(CodeAlreadyExists, "already exists")
	ErrConflict         = New(CodeConflict, "conflict")
	ErrValidation       = New(CodeValidation, "validation failed")
	ErrPermissionDenied = New(CodePermissionDenied, "permission denied")
	ErrInternal         = New(CodeInternal, "internal error")
)

// Code returns the stable type code.
func (e *Error) Code() Code { return e.code }

// RawCode returns the unrecognized code string; empty for known codes.
func (e *Error) RawCode() string { return e.rawCode }

// Message returns the human-readable message.
func (e *Error) Message() string { return e.msg }

// Object returns the affected object name ("" when absent).
func (e *Error) Object() string { return e.object }

// Property returns the affected property name ("" when absent).
func (e *Error) Property() string { return e.property }

// Cause returns the underlying cause, possibly nil.
func (e *Error) Cause() error { return e.cause }

// Unwrap exposes the cause so errors.Is/errors.As traverse the chain.
func (e *Error) Unwrap() error { return e.cause }

// Error renders message with object/property context and the cause.
func (e *Error) Error() string {
	s := e.msg
	switch {
	case e.object != "" && e.property != "":
		s = fmt.Sprintf("%s (object=%s, property=%s)", s, e.object, e.property)
	case e.object != "":
		s = fmt.Sprintf("%s (object=%s)", s, e.object)
	}
	if e.cause != nil {
		s = s + ": " + e.cause.Error()
	}
	return s
}

// Is implements the type hierarchy. Matching the base class always succeeds;
// otherwise the concrete codes must agree.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	isCompareCount++
	if t.code == CodeUnknown {
		return true // concrete error Is-A ErrBase
	}
	return e.code == t.code
}

// CauseDepth returns the length of the Unwrap chain rooted at err.
func CauseDepth(err error) int {
	depth := 0
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return depth
		}
		depth++
		err = next
	}
	return depth
}
