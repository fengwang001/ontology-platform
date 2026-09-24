// Package netmatch parses IP addresses and prefixes and tests containment.
package netmatch

import (
	"errors"
	"net"
	"net/netip"
)

var (
	ErrBadAddress = errors.New("netmatch: invalid address")
	ErrBadPrefix  = errors.New("netmatch: invalid prefix")
)

// ParseAddress accepts IPv4/IPv6 text, optionally with port and, for IPv6,
// brackets and a zone. Ports and zones are dropped.
func ParseAddress(s string) (netip.Addr, error) {
	host := s
	if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		if h2, err2 := netip.ParseAddr(s); err2 == nil {
			addr = h2
		} else {
			return netip.Addr{}, ErrBadAddress
		}
	}
	return addr.WithZone(""), nil
}

// ParsePrefix parses an address-family prefix length. An over-long prefix
// length is illegal.
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, ErrBadPrefix
	}
	bits := p.Addr().BitLen()
	if p.Bits() > bits {
		return netip.Prefix{}, ErrBadPrefix
	}
	return p.Masked(), nil
}

// Contains reports whether addr belongs to p. IPv4-mapped IPv6 addresses are
// treated as their IPv4 address, so an IPv4 prefix matches them (and the
// mapped-IPv6 prefix does too).
func Contains(p netip.Prefix, addr netip.Addr) bool {
	addr = addr.WithZone("")
	switch {
	case p.Addr().Is4() && addr.Is4In6():
		addr = addr.Unmap()
	case !p.Addr().Is4() && addr.Is4():
		addr = netip.AddrFrom16(addr.As16())
	}
	return p.Contains(addr)
}
