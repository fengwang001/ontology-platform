// Package align provides alignment arithmetic, header-offset computation
// and request validation for the aligned allocator. It depends on nothing.
package align

import "errors"

// HeaderSize is the fixed size in bytes of the bookkeeping header that sits
// immediately before every returned pointer.
const HeaderSize = 8

// Sentinel errors for rejected requests.
var (
	ErrInvalidSize  = errors.New("align: size must be >= 1")
	ErrInvalidAlign = errors.New("align: align must be a power of two >= 1")
)

// Check validates an Alloc request: size >= 1 and align a power of two >= 1.
func Check(size, a int) error {
	if size < 1 {
		return ErrInvalidSize
	}
	if a < 1 || a&(a-1) != 0 {
		return ErrInvalidAlign
	}
	return nil
}

// Up returns the smallest multiple of a that is >= v. a must be a power of
// two (enforced by Check); the bit trick is only valid for powers of two.
func Up(v, a int) int {
	return (v + a - 1) &^ (a - 1)
}

// HeaderOff returns the offset at which the header for ptr is stored.
func HeaderOff(ptr int) int {
	return ptr - HeaderSize
}
