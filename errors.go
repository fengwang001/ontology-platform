package ontology

import (
	"errors"
	"fmt"
)

// ErrNegativeTokens is returned when a negative token count is requested.
// It is a caller bug (invalid argument), a different category from
// "not enough tokens", which is not an error at all.
var ErrNegativeTokens = errors.New("ontology: token count must not be negative")

// ErrExceedsCapacity matches requests for more tokens than a bucket can
// ever hold. Such requests can never be satisfied no matter how long the
// caller waits, so they are rejected immediately instead of blocking.
// Use errors.Is(err, ErrExceedsCapacity) to detect this category, and
// errors.As to recover the details as a *CapacityError.
var ErrExceedsCapacity = errors.New("ontology: requested tokens exceed bucket capacity")

// CapacityError describes a request that exceeds the bucket capacity.
type CapacityError struct {
	Tenant    string
	Requested int64
	Capacity  int64
}

// Error implements the error interface.
func (e *CapacityError) Error() string {
	return fmt.Sprintf("ontology: tenant %q requested %d tokens but capacity is %d (can never be satisfied)",
		e.Tenant, e.Requested, e.Capacity)
}

// Is reports whether target is ErrExceedsCapacity.
func (e *CapacityError) Is(target error) bool { return target == ErrExceedsCapacity }
