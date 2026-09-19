package ontology

import (
	"errors"
	"time"
)

var (
	// ErrNegativeTokens indicates that a request asked for a negative token count.
	ErrNegativeTokens = errors.New("ontology: requested token count must not be negative")

	// ErrRequestExceedsCapacity indicates that the request can never fit.
	ErrRequestExceedsCapacity = errors.New("ontology: requested token count exceeds bucket capacity")

	// ErrClockMovedBack indicates that now was earlier than the bucket's prior time.
	ErrClockMovedBack = errors.New("ontology: injected clock moved backwards")

	// ErrUnknownTenant indicates that no bucket configuration exists.
	ErrUnknownTenant = errors.New("ontology: unknown tenant")

	// ErrTenantAlreadyExists indicates a duplicate tenant registration.
	ErrTenantAlreadyExists = errors.New("ontology: tenant already exists")

	// ErrInvalidConfig indicates an unusable bucket configuration.
	ErrInvalidConfig = errors.New("ontology: invalid rate limiter configuration")

	// ErrTokensUnavailable marks a normal, retryable token shortage.
	ErrTokensUnavailable = errors.New("ontology: insufficient tokens")
)

// DeniedError describes a retryable shortage and the earliest useful retry time.
type DeniedError struct {
	Requested int64
	Available int64
	Wait      time.Duration
}

func (e *DeniedError) Error() string {
	return "ontology: insufficient tokens; retry after " + e.Wait.String()
}

func (e *DeniedError) Is(target error) bool {
	return target == ErrTokensUnavailable
}

// AsDenied extracts retry information from a normal token-shortage error.
func AsDenied(err error) (*DeniedError, bool) {
	var denied *DeniedError
	if errors.As(err, &denied) {
		return denied, true
	}
	return nil, false
}
