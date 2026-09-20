// Package argv implements a small command-line argument parser.
package argv

import "errors"

// Kind describes whether a flag takes a value.
type Kind int

const (
	// Bool flags take no value; their presence means true.
	Bool Kind = iota
	// String flags require a value.
	String
)

// Spec declares a single flag.
type Spec struct {
	Long     string // long name, e.g. "output"
	Short    rune   // short name, e.g. 'o'; zero means none
	Kind     Kind
	Required bool
	Default  string // only used by String flags
}

var (
	ErrUnknownFlag  = errors.New("argv: unknown flag")
	ErrMissingValue = errors.New("argv: missing value")
	ErrRequired     = errors.New("argv: required flag missing")
	ErrDuplicate    = errors.New("argv: duplicate flag")
)
