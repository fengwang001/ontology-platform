package iproute

import "errors"

var (
	ErrInvalidIP      = errors.New("iproute: invalid IPv4 address")
	ErrInvalidCIDR    = errors.New("iproute: invalid CIDR notation")
	ErrInvalidMaskLen = errors.New("iproute: invalid prefix length")
	ErrHostBitsSet    = errors.New("iproute: host bits must be zero")
	ErrEmptyNext      = errors.New("iproute: next hop must not be empty")
	ErrDuplicate      = errors.New("iproute: prefix already exists")
	ErrNotFound       = errors.New("iproute: prefix not found")
	ErrNoRoute        = errors.New("iproute: no matching route")
)
