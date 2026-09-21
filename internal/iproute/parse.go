package iproute

import (
	"fmt"
	"strings"
)

// prefix is a parsed, canonical IPv4 CIDR: network address plus mask length.
type prefix struct {
	addr uint32 // network address, host bits guaranteed zero
	len  int    // mask length in [0,32]
}

// mask returns the network mask for a prefix length in [0,32].
func mask(length int) uint32 {
	if length == 0 {
		return 0
	}
	return ^uint32(0) << (32 - length)
}

// parseIP parses a dotted-quad IPv4 address into its 32-bit value.
// Octets must be decimal, in [0,255], with no leading zeros and no
// surrounding whitespace.
func parseIP(s string) (uint32, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return 0, fmt.Errorf("%w: %q must have 4 octets", ErrInvalidIP, s)
	}
	var addr uint32
	for _, p := range parts {
		v, err := parseOctet(p)
		if err != nil {
			return 0, err
		}
		addr = addr<<8 | uint32(v)
	}
	return addr, nil
}

// parseOctet parses one decimal octet, rejecting empty strings, non-digits,
// leading zeros and values above 255.
func parseOctet(s string) (uint32, error) {
	if len(s) == 0 || len(s) > 3 {
		return 0, fmt.Errorf("%w: bad octet %q", ErrInvalidIP, s)
	}
	var v uint32
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%w: octet %q is not decimal", ErrInvalidIP, s)
		}
		v = v*10 + uint32(s[i]-'0')
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: octet %q has a leading zero", ErrInvalidIP, s)
	}
	if v > 255 {
		return 0, fmt.Errorf("%w: octet %q exceeds 255", ErrInvalidIP, s)
	}
	return v, nil
}

// parsePrefix parses a CIDR string of the form a.b.c.d/len. The mask length
// must be a decimal integer in [0,32] without leading zeros, and all host
// bits of the address must be zero.
func parsePrefix(cidr string) (prefix, error) {
	slash := strings.IndexByte(cidr, '/')
	if slash < 0 || strings.IndexByte(cidr[slash+1:], '/') >= 0 {
		return prefix{}, fmt.Errorf("%w: %q must contain exactly one '/'", ErrInvalidCIDR, cidr)
	}
	addr, err := parseIP(cidr[:slash])
	if err != nil {
		return prefix{}, err
	}
	length, err := parseMaskLen(cidr[slash+1:])
	if err != nil {
		return prefix{}, err
	}
	if addr&^mask(length) != 0 {
		return prefix{}, fmt.Errorf("%w: %q has non-zero host bits", ErrHostBitsSet, cidr)
	}
	return prefix{addr: addr, len: length}, nil
}

// parseMaskLen parses the decimal prefix length after the slash.
func parseMaskLen(s string) (int, error) {
	if len(s) == 0 || len(s) > 2 {
		return 0, fmt.Errorf("%w: bad mask length %q", ErrInvalidCIDR, s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: mask length %q has a leading zero", ErrInvalidCIDR, s)
	}
	var v int
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%w: mask length %q is not decimal", ErrInvalidCIDR, s)
		}
		v = v*10 + int(s[i]-'0')
	}
	if v > 32 {
		return 0, fmt.Errorf("%w: mask length %q exceeds 32", ErrInvalidCIDR, s)
	}
	return v, nil
}

// canonical returns the standard string form of the prefix, e.g. "10.1.0.0/16".
func (p prefix) canonical() string {
	return fmt.Sprintf("%d.%d.%d.%d/%d",
		p.addr>>24, p.addr>>16&0xff, p.addr>>8&0xff, p.addr&0xff, p.len)
}
