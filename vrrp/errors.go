package vrrp

import "errors"

var (
	ErrInvalidArgument  = errors.New("vrrp: invalid argument")
	ErrClockRegression  = errors.New("vrrp: clock regression")
	ErrNotStarted       = errors.New("vrrp: device not started")
	ErrIdentityConflict = errors.New("vrrp: identity conflict")
)
