// Package align holds the pure arithmetic and argument validation for the
// alignment allocator. It must not depend on any other package in this module.
package align

import "errors"

// HeaderSize is the fixed size, in bytes, of the header placed immediately
// before every returned pointer. It is given by the task and must not change.
const HeaderSize = 8

// Sentinel errors for the two kinds of invalid requests. They are distinct so
// callers can tell them apart with errors.Is.
var (
	ErrBadSize  = errors.New("align: size must be >= 1")
	ErrBadAlign = errors.New("align: align must be a power of two and >= 1")
)

// AlignUp returns the smallest value >= x that is a multiple of a.
// a must be a power of two (callers gate on Validate).
func AlignUp(x, a int) int {
	return (x + a - 1) &^ (a - 1)
}

// HeaderOffset is the byte offset at which the 8-byte header for ptr lives,
// i.e. the start of the half-open interval [ptr-HeaderSize, ptr).
func HeaderOffset(ptr int) int {
	return ptr - HeaderSize
}

// Validate checks size >= 1 and that align is a power of two >= 1. Size is
// checked first. It mutates nothing and returns a distinct sentinel per cause.
func Validate(size, align int) error {
	if size < 1 {
		return ErrBadSize
	}
	if align < 1 || align&(align-1) != 0 {
		return ErrBadAlign
	}
	return nil
}
