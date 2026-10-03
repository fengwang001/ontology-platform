package org

import "errors"

var (
	ErrInvalid = errors.New("org: invalid argument")
	ErrCycle   = errors.New("org: manager chain would contain a cycle")
)
