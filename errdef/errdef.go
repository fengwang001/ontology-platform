// Package errdef defines the typed error taxonomy of the ontology.
package errdef

import "fmt"

// Code is the stable wire identity of an error type.
type Code string

const (
	CodeBase             Code = "base"
	CodeNotFound         Code = "not_found"
	CodeAlreadyExists    Code = "already_exists"
	CodeConflict         Code = "conflict"
	CodeValidation       Code = "validation"
	CodePermissionDenied Code = "permission_denied"
	CodeInternal         Code = "internal"
)

// CodeProvider exposes the type identity without requiring *Base.
type CodeProvider interface{ ErrCode() Code }

// ContextProvider exposes object/property context attached to an error.
type ContextProvider interface {
	Object() string
	Property() string
}

// isSteps counts identity comparisons, proving Is stays O(1) in wrap depth.
var isSteps int

// ResetSteps zeroes the non-exported comparison counter (test hook).
func ResetSteps() { isSteps = 0 }

// Steps returns the number of identity comparisons since ResetSteps.
func Steps() int { return isSteps }

// Base is the wide base type every concrete error embeds.
type Base struct {
	code     Code
	message  string
	object   string
	property string
	cause    error
}

// ErrCode returns the stable type code.
func (b *Base) ErrCode() Code { return b.code }

// Error renders message with object/property context.
func (b *Base) Error() string {
	switch {
	case b.object != "" && b.property != "":
		return fmt.Sprintf("%s: %s.%s", b.message, b.object, b.property)
	case b.object != "":
		return fmt.Sprintf("%s: %s", b.message, b.object)
	default:
		return b.message
	}
}

// Object returns the referenced object ("" when absent).
func (b *Base) Object() string { return b.object }

// Property returns the referenced property ("" when absent).
func (b *Base) Property() string { return b.property }

// Message returns the raw message without the context suffix.
func (b *Base) Message() string { return b.message }

// Cause returns the wrapped cause, or nil.
func (b *Base) Cause() error { return b.cause }

// Unwrap exposes the cause so errors.Is/As traverse the chain.
func (b *Base) Unwrap() error { return b.cause }

// Is matches by code. The wide base sentinel matches any typed error,
// and equal codes match regardless of concrete wrapper struct.
func (b *Base) Is(target error) bool {
	isSteps++
	tp, ok := target.(CodeProvider)
	if !ok {
		return false
	}
	tc := tp.ErrCode()
	return tc == b.code || tc == CodeBase
}

// As exposes the embedded *Base so errors.As(err, &base) works,
// complementing concrete-type extraction across the hierarchy.
func (b *Base) As(target any) bool {
	if bp, ok := target.(**Base); ok {
		*bp = b
		return true
	}
	return false
}

// Option configures an error at construction.
type Option func(*Base)

// WithObject attaches the failing object id.
func WithObject(id string) Option { return func(b *Base) { b.object = id } }

// WithProperty attaches the failing property name.
func WithProperty(p string) Option { return func(b *Base) { b.property = p } }

// WithCause chains an underlying error.
func WithCause(cause error) Option { return func(b *Base) { b.cause = cause } }

// GenericError carries any code, used for unknown/future codes on decode.
type GenericError struct{ *Base }

// Concrete types embed *Base and inherit Is/As/Unwrap/context.

type NotFoundError struct{ *Base }
type AlreadyExistsError struct{ *Base }
type ConflictError struct{ *Base }
type ValidationError struct{ *Base }
type PermissionDeniedError struct{ *Base }
type InternalError struct{ *Base }

func newBase(code Code, msg string, opts ...Option) *Base {
	b := &Base{code: code, message: msg}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// New builds the concrete type registered for code.
// CodeBase and unknown codes yield *GenericError.
func New(code Code, msg string, opts ...Option) error {
	return newByCode(code, newBase(code, msg, opts...))
}

func newByCode(code Code, b *Base) error {
	switch code {
	case CodeNotFound:
		return &NotFoundError{b}
	case CodeAlreadyExists:
		return &AlreadyExistsError{b}
	case CodeConflict:
		return &ConflictError{b}
	case CodeValidation:
		return &ValidationError{b}
	case CodePermissionDenied:
		return &PermissionDeniedError{b}
	case CodeInternal:
		return &InternalError{b}
	default:
		return &GenericError{b}
	}
}

// Rebuild reconstructs a concrete error from an already populated Base.
// It is used by decoders that recover code/message/context/cause from the wire.
func Rebuild(code Code, msg, object, property string, cause error) error {
	return newByCode(code, &Base{
		code: code, message: msg, object: object, property: property, cause: cause,
	})
}

// Typed constructors.

func NotFound(msg string, opts ...Option) error {
	return &NotFoundError{newBase(CodeNotFound, msg, opts...)}
}

func AlreadyExists(msg string, opts ...Option) error {
	return &AlreadyExistsError{newBase(CodeAlreadyExists, msg, opts...)}
}

func Conflict(msg string, opts ...Option) error {
	return &ConflictError{newBase(CodeConflict, msg, opts...)}
}

func Validation(msg string, opts ...Option) error {
	return &ValidationError{newBase(CodeValidation, msg, opts...)}
}

func PermissionDenied(msg string, opts ...Option) error {
	return &PermissionDeniedError{newBase(CodePermissionDenied, msg, opts...)}
}

func Internal(msg string, opts ...Option) error {
	return &InternalError{newBase(CodeInternal, msg, opts...)}
}

// Sentinel type identities. errors.Is(err, ErrNotFound) asks the
// "is this a not-found-class error?" question along the whole chain.

var (
	ErrBase             = GenericError{&Base{code: CodeBase, message: "error"}}
	ErrNotFound         = NotFoundError{&Base{code: CodeNotFound, message: "not found"}}
	ErrAlreadyExists    = AlreadyExistsError{&Base{code: CodeAlreadyExists, message: "already exists"}}
	ErrConflict         = ConflictError{&Base{code: CodeConflict, message: "conflict"}}
	ErrValidation       = ValidationError{&Base{code: CodeValidation, message: "validation failed"}}
	ErrPermissionDenied = PermissionDeniedError{&Base{code: CodePermissionDenied, message: "permission denied"}}
	ErrInternal         = InternalError{&Base{code: CodeInternal, message: "internal error"}}
)
