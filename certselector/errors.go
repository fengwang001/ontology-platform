package certselector

import "errors"

var (
	// ErrInvalidArgument means an argument is malformed.
	ErrInvalidArgument = errors.New("certselector: invalid argument")
	// ErrConflict means the certificate ID already exists.
	ErrConflict = errors.New("certselector: conflict")
	// ErrNotFound means the referenced certificate does not exist.
	ErrNotFound = errors.New("certselector: not found")
	// ErrNoMatch means no certificate matched and no default was set.
	ErrNoMatch = errors.New("certselector: no matching certificate")
	// ErrExpired means matching certificates exist but none is in its validity interval.
	ErrExpired = errors.New("certselector: all matching certificates out of validity interval")
	// ErrUnsupportedKey means a time-valid matching certificate exists but its key type is unsupported.
	ErrUnsupportedKey = errors.New("certselector: matching key types not supported")
)
