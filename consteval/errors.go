package consteval

import "fmt"

// ErrKind classifies evaluation and registration failures. The declaration
// order reflects the mandated priority: structural errors outrank duplicate
// registration, which outranks unknown names, which outrank evaluation
// errors; within one node type mismatch outranks illegal operation, which
// outranks division by zero, which outranks representability failures.
type ErrKind int

const (
	// ErrInvalidArgument: malformed tree (arity mismatch, unknown operator,
	// unknown type name, empty name reference) or empty constant name.
	ErrInvalidArgument ErrKind = iota
	// ErrDuplicateName: a constant with this name is already registered.
	ErrDuplicateName
	// ErrUnknownName: the expression references an unregistered name.
	ErrUnknownName
	// ErrTypeMismatch: operand kinds or concrete types are incompatible.
	ErrTypeMismatch
	// ErrIllegalOp: the operator does not support these kinds, or a shift
	// count is negative / above 1000 / not of integer kind.
	ErrIllegalOp
	// ErrDivByZero: division or remainder with a zero divisor.
	ErrDivByZero
	// ErrOverflow: a value falls outside the target type's range, or a
	// float conversion rounds to infinity.
	ErrOverflow
	// ErrTruncation: conversion to an integer type of a non-integral value.
	ErrTruncation
	// ErrConstTooLarge: an untyped integer exceeds 512 bits of magnitude.
	ErrConstTooLarge
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
	case ErrConstTooLarge:
		return "constant too large"
	}
	return "unknown error"
}

// Error is the single error type produced by this package. Kind carries the
// category (and thus the priority); Msg describes the concrete failure.
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
