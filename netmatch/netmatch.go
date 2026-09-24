// Package netmatch parses IP addresses and CIDR prefixes and tests
// whether an address is contained in a prefix.
package netmatch

import (
	"errors"
	"net"
	"net/netip"
	"strings"
)

var (
	// ErrBadAddr reports address text that cannot be parsed.
	ErrBadAddr = errors.New("netmatch: invalid address")
	// ErrBadCIDR reports a prefix whose length exceeds its family bits
	// or that otherwise cannot be parsed.
	ErrBadCIDR = errors.New("netmatch: invalid cidr")
)

// ParseAddr parses IPv4/IPv6 text, stripping any port and zone.
// Accepts "1.2.3.4:80", "[::1]:8080", "fe80::1%eth0" and bare forms.
func ParseAddr(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, ErrBadAddr
	}
	switch {
	case strings.HasPrefix(s, "["):
		host, _, err := net.SplitHostPort(s)
		if err != nil {
			end := strings.LastIndex(s, "]")
			if end < 0 || strings.TrimSpace(s[end+1:]) != "" {
				return netip.Addr{}, ErrBadAddr
			}
			host = s[1:end]
		}
		s = host
	case strings.Count(s, ":") == 1:
		if host, _, err := net.SplitHostPort(s); err == nil {
			s = host
		}
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, ErrBadAddr
	}
	return a.WithZone(""), nil
}

// ParseCIDR parses a prefix-length network such as "10.0.0.0/8".
// A prefix length beyond the address family bits is invalid.
func ParseCIDR(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, ErrBadCIDR
	}
	return p, nil
}

// Contains reports whether a is inside p. An IPv4-mapped IPv6 address
// matches both the IPv4 prefix of the same network and IPv6 prefixes.
func Contains(p netip.Prefix, a netip.Addr) bool {
	a = a.WithZone("")
	if p.Contains(a) {
		return true
	}
	if a.Is4In6() && p.Contains(a.Unmap()) {
		return true
	}
	if a.Is4() || a.Is4In6() {
		v4 := a.Unmap().As4()
		var b [16]byte
		b[10], b[11] = 0xff, 0xff
		copy(b[12:], v4[:])
		return p.Contains(netip.AddrFrom16(b))
	}
	return false
}
