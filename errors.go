package resolver

import "errors"

var (
	ErrInvalidVersion   = errors.New("invalid version")
	ErrInvalidRange     = errors.New("invalid dependency range")
	ErrDuplicateVersion = errors.New("duplicate package version")
	ErrVersionNotFound  = errors.New("version not found")
	ErrPackageNotFound  = errors.New("package not found")
	ErrNoSolution       = errors.New("no compatible solution")
)
