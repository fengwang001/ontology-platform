// Package argv implements a small command-line argument parser.
//
// It supports long flags (--output=x / --output x), short flags
// (-o x / -ox), bundling of boolean short flags (-abc), the "--"
// terminator, and interleaved positional operands. It does not wrap
// the standard library flag package.
package argv

import "errors"

// Kind describes the value type of a flag.
type Kind int

const (
	// Bool flags take no value; their presence means true.
	Bool Kind = iota
	// String flags require a value.
	String
)

// Spec declares a single command-line flag.
type Spec struct {
	Long     string // long name, e.g. "output"
	Short    rune   // short name, e.g. 'o'; zero means none
	Kind     Kind
	Required bool
	Default  string // String only
}

// Sentinel errors returned by Parse. Use errors.Is to classify.
var (
	ErrUnknownFlag  = errors.New("argv: unknown flag")
	ErrMissingValue = errors.New("argv: missing value")
	ErrRequired     = errors.New("argv: required flag not provided")
	ErrDuplicate    = errors.New("argv: flag provided more than once")
)
