// Package iproute implements an IPv4 CIDR longest-prefix-match routing
// table without relying on the net or net/netip packages.
package iproute

import "errors"

var (
	// ErrInvalidIP is returned when an IP address string is not a
	// dotted-quad of four decimal octets in [0,255] without leading zeros.
	ErrInvalidIP = errors.New("iproute: invalid IPv4 address")

	// ErrInvalidCIDR is returned when a CIDR string is not of the form
	// a.b.c.d/len with len a decimal integer in [0,32] without leading zeros.
	ErrInvalidCIDR = errors.New("iproute: invalid CIDR notation")

	// ErrHostBitsSet is returned when a CIDR has non-zero host bits,
	// e.g. 192.168.1.1/24. The table never normalizes silently.
	ErrHostBitsSet = errors.New("iproute: host bits must be zero")

	// ErrEmptyNext is returned when Add is called with an empty next hop.
	ErrEmptyNext = errors.New("iproute: next hop must not be empty")

	// ErrDuplicateRoute is returned when Add targets a prefix that
	// already exists in the table.
	ErrDuplicateRoute = errors.New("iproute: route already exists")

	// ErrRouteNotFound is returned when Delete targets a prefix that
	// does not exist in the table.
	ErrRouteNotFound = errors.New("iproute: route not found")

	// ErrNoRoute is returned by Lookup when no prefix matches the
	// queried address.
	ErrNoRoute = errors.New("iproute: no matching route")
)
