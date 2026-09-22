// Package step defines a single SAGA step: its forward action, compensation,
// retry budget and idempotency key. It depends on no other package.
package step

import (
	"context"
	"errors"
	"fmt"
)

// ErrDefiniteFailure marks an error whose side effect is guaranteed not to
// have happened. Any other error is treated as "unknown / possibly applied".
var ErrDefiniteFailure = errors.New("step: definite failure")

// ErrNilCompensation is reported when a step that produced an effect has no
// compensation action.
var ErrNilCompensation = errors.New("step: compensation action is nil")

// Action is one physical invocation. idemKey is the step idempotency key,
// attempt is the 0-based physical attempt number, nowMs is injected clock.
type Action func(ctx context.Context, idemKey string, attempt int, nowMs int64) error

// Step is one ordered SAGA step.
type Step struct {
	Name        string
	IdemKey     string
	Retries     int // extra physical attempts after the first
	Do          Action
	Compensate  Action
}

// Definite wraps cause so the failure is classified as "definitely not applied".
func Definite(cause error) error {
	if cause == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrDefiniteFailure, cause)
}

// IsDefinite reports whether err is a definite (effect-free) failure.
func IsDefinite(err error) bool { return errors.Is(err, ErrDefiniteFailure) }
