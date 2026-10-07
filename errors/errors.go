// Package errors provides normalized error kinds for action invocation.
//
// Three distinguishable categories, reported in this priority order:
//  1. KindInvalidArgument: invalid parameters (missing target instance,
//     write payload type mismatch)
//  2. KindPreHook: pre-validation hook failure
//  3. KindPostHook: aggregated post-commit-validation hook failures
package errors

import "strings"

// Kind identifies a normalized error category.
type Kind int

const (
	KindInvalidArgument Kind = iota
	KindPreHook
	KindPostHook
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid-argument"
	case KindPreHook:
		return "pre-hook"
	case KindPostHook:
		return "post-hook"
	}
	return "unknown"
}

// HookFailure describes the failure of a single hook.
type HookFailure struct {
	TypeName string
	HookName string
	Message  string
}

// Error is a normalized action invocation error.
type Error struct {
	Kind     Kind
	Action   string
	Message  string
	Failures []HookFailure // non-empty only for KindPostHook, in registration order
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.String())
	b.WriteString(": action ")
	b.WriteString(e.Action)
	b.WriteString(": ")
	b.WriteString(e.Message)
	for _, f := range e.Failures {
		b.WriteString("\n  - ")
		b.WriteString(f.TypeName)
		b.WriteString("/")
		b.WriteString(f.HookName)
		b.WriteString(": ")
		b.WriteString(f.Message)
	}
	return b.String()
}

// InvalidArgument builds a KindInvalidArgument error.
func InvalidArgument(action, msg string) *Error {
	return &Error{Kind: KindInvalidArgument, Action: action, Message: msg}
}

// PreHook builds a KindPreHook error.
func PreHook(action, msg string) *Error {
	return &Error{Kind: KindPreHook, Action: action, Message: msg}
}

// PostHook builds an aggregated KindPostHook error.
func PostHook(action string, failures []HookFailure) *Error {
	return &Error{Kind: KindPostHook, Action: action, Message: "post-commit validation failed", Failures: failures}
}

// AsKind extracts the normalized kind from err; ok is false when err is
// not a normalized error.
func AsKind(err error) (Kind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}
