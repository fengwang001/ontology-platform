package sla

import "errors"

var (
	ErrInvalid  = errors.New("sla: invalid argument")
	ErrNotFound = errors.New("sla: timer not found")
	ErrExists   = errors.New("sla: timer already exists")
	ErrState    = errors.New("sla: illegal state transition")
)
