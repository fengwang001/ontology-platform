package authz

import "errors"

var (
	ErrInvalid   = errors.New("authz: invalid argument")
	ErrNotFound  = errors.New("authz: not found")
	ErrState     = errors.New("authz: illegal state")
	ErrConflict  = errors.New("authz: conflict")
	ErrNoApprove = errors.New("authz: approve permission required")
	ErrRotate    = errors.New("authz: approver must differ from counters")
	ErrNoSenior  = errors.New("authz: senior permission required")
	ErrStock     = errors.New("authz: insufficient stock")
)
