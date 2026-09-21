package iproute

import "errors"

var (
	ErrInvalidIP      = errors.New("iproute: invalid IPv4 address")
	ErrInvalidCIDR    = errors.New("iproute: invalid CIDR notation")
	ErrInvalidMask    = errors.New("iproute: invalid prefix length")
	ErrHostBitsSet    = errors.New("iproute: host bits must be zero")
	ErrEmptyNext      = errors.New("iproute: next hop must not be empty")
	ErrDuplicateRoute = errors.New("iproute: route already exists")
	ErrRouteNotFound  = errors.New("iproute: route not found")
	ErrNoRoute        = errors.New("iproute: no matching route")
)
