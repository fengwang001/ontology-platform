package iproute

import (
	"fmt"
	"strings"
)

// parseIPv4 parses a strict dotted-quad IPv4 address into a uint32.
// Each octet must be decimal, 0-255, with no leading zeros and no
// surrounding whitespace.
func parseIPv4(s string) (uint32, error) {
	if s == "" || s != strings.TrimSpace(s) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidIP, s)
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidIP, s)
	}
	var addr uint32
	for _, p := range parts {
		octet, err := parseOctet(p)
		if err != nil {
			return 0, err
		}
		addr = addr<<8 | uint32(octet)
	}
	return addr, nil
}

func parseOctet(s string) (uint32, error) {
	if s == "" || len(s) > 3 {
		return 0, fmt.Errorf("%w: bad octet %q", ErrInvalidIP, s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: leading zero in octet %q", ErrInvalidIP, s)
	}
	var v uint32
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: non-digit in octet %q", ErrInvalidIP, s)
		}
		v = v*10 + uint32(c-'0')
	}
	if v > 255 {
		return 0, fmt.Errorf("%w: octet %q out of range", ErrInvalidIP, s)
	}
	return v, nil
}

// parseMaskLen parses a prefix length: decimal 0-32, no leading zeros.
func parseMaskLen(s string) (int, error) {
	if s == "" || len(s) > 2 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMask, s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: leading zero in %q", ErrInvalidMask, s)
	}
	var v int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: non-digit in %q", ErrInvalidMask, s)
		}
		v = v*10 + int(c-'0')
	}
	if v > 32 {
		return 0, fmt.Errorf("%w: %q out of range 0-32", ErrInvalidMask, s)
	}
	return v, nil
}

// prefix is a parsed CIDR: network address plus prefix length.
type prefix struct {
	addr uint32 // network address, host bits already zero
	bits int    // prefix length 0-32
}

// parseCIDR parses "a.b.c.d/len" and enforces zero host bits.
func parseCIDR(s string) (prefix, error) {
	slash := strings.IndexByte(s, '/')
	if slash < 0 || strings.IndexByte(s[slash+1:], '/') >= 0 {
		return prefix{}, fmt.Errorf("%w: %q", ErrInvalidCIDR, s)
	}
	addr, err := parseIPv4(s[:slash])
	if err != nil {
		return prefix{}, err
	}
	bits, err := parseMaskLen(s[slash+1:])
	if err != nil {
		return prefix{}, err
	}
	mask := maskFor(bits)
	if addr&^mask != 0 {
		return prefix{}, fmt.Errorf("%w: %q", ErrHostBitsSet, s)
	}
	return prefix{addr: addr, bits: bits}, nil
}

// maskFor returns the network mask for a prefix length.
func maskFor(bits int) uint32 {
	if bits == 0 {
		return 0
	}
	return ^uint32(0) << (32 - bits)
}

// canonical returns the canonical string form, e.g. "10.1.0.0/16".
func (p prefix) canonical() string {
	return fmt.Sprintf("%d.%d.%d.%d/%d",
		p.addr>>24&0xff, p.addr>>16&0xff, p.addr>>8&0xff, p.addr&0xff,
		p.bits)
}
