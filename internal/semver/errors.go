package semver

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by this package. Use errors.Is to test for them.
var (
	// ErrInvalidVersion indicates a string is not a valid SemVer 2.0.0 version.
	ErrInvalidVersion = errors.New("semver: invalid version")
	// ErrInvalidRange indicates a string is not a valid range constraint.
	ErrInvalidRange = errors.New("semver: invalid range constraint")
	// ErrEmptyIntersection indicates a set of ranges has an empty intersection.
	ErrEmptyIntersection = errors.New("semver: empty intersection")
)

// ParseError describes a malformed version or range string.
type ParseError struct {
	Kind  string // "version" or "range"
	Input string
	Err   error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("semver: cannot parse %s %q: %v", e.Kind, e.Input, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// ConflictError reports that two concrete constraints cannot both be
// satisfied. A and B hold the constraints exactly as originally written.
type ConflictError struct {
	A string
	B string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("semver: constraints %q and %q conflict: no version can satisfy both", e.A, e.B)
}

// Unwrap exposes ErrEmptyIntersection so errors.Is works on ConflictError.
func (e *ConflictError) Unwrap() error { return ErrEmptyIntersection }
