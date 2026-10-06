// Package swcoord implements a service-worker style coordinator that
// manages registrations keyed by scope, version lifecycles
// (installing/waiting/active/redundant), client control, update checks
// and per-version cache manifests.
package swcoord

import (
	"errors"
	"fmt"
)

// Rejection categories, checked in this exact precedence order:
// invalid argument -> clock rollback -> registration not found ->
// version not found -> state not allowed -> too frequent.
var (
	ErrInvalidArgument      = errors.New("swcoord: invalid argument")
	ErrClockRollback        = errors.New("swcoord: clock rollback")
	ErrRegistrationNotFound = errors.New("swcoord: registration not found")
	ErrVersionNotFound      = errors.New("swcoord: version not found")
	ErrStateNotAllowed      = errors.New("swcoord: state not allowed")
	ErrTooFrequent          = errors.New("swcoord: too frequent")
)

func invalidArgf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}

func stateNotAllowedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrStateNotAllowed, fmt.Sprintf(format, args...))
}
