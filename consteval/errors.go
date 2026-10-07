package consteval

import "fmt"

// ErrKind classifies evaluation failures. The declaration order reflects
// the reporting priority groups defined by the language:
//
//	invalid argument > duplicate name > unknown name >
//	type mismatch > illegal operation > division by zero >
//	unrepresentable (overflow / truncation / constant too large)
//
// Within a single node, higher-priority errors mask lower-priority ones;
// across the tree, errors are reported in left-to-right, children-first
// evaluation order.
type ErrKind int

const (
	// ErrInvalidArgument: malformed tree (operator/operand count
	// mismatch, unknown type name, reference to an empty name) or an
	// empty constant name at registration.
	ErrInvalidArgument ErrKind = iota
	// ErrDuplicateName: registering an already-registered name.
	ErrDuplicateName
	// ErrUnknownName: referencing a name that is not registered.
	ErrUnknownName
	// ErrTypeMismatch: operand kinds/types are incompatible, or two
	// typed operands have different types.
	ErrTypeMismatch
	// ErrIllegalOp: the operator does not support the operand kind
	// (including illegal shifts).
	ErrIllegalOp
	// ErrDivByZero: division or remainder with a zero divisor.
	ErrDivByZero
	// ErrOverflow: a value is outside the range of its type.
	ErrOverflow
	// ErrTruncation: conversion to an integer type of a non-integral
	// value.
	ErrTruncation
	// ErrTooLarge: an untyped integer exceeds the 512-bit limit.
	ErrTooLarge
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrDuplicateName:
		return "duplicate name"
	case ErrUnknownName:
		return "unknown name"
	case ErrTypeMismatch:
		return "type mismatch"
	case ErrIllegalOp:
		return "illegal operation"
	case ErrDivByZero:
		return "division by zero"
	case ErrOverflow:
		return "overflow"
	case ErrTruncation:
		return "truncation"
	case ErrTooLarge:
		return "constant too large"
	}
	return "unknown error"
}

// Error is the single error type returned by this package. Kind carries
// the classification; Detail gives human-readable context.
type Error struct {
	Kind   ErrKind
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Kind.String()
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}
