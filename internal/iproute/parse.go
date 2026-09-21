package iproute

import (
	"fmt"
	"strings"
)

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
			return 0, fmt.Errorf("%w: %q", ErrInvalidIP, s)
		}
		addr = addr<<8 | uint32(octet)
	}
	return addr, nil
}

func parseOctet(s string) (uint8, error) {
	if len(s) == 0 || len(s) > 3 {
		return 0, ErrInvalidIP
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalidIP
	}
	var v uint16
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalidIP
		}
		v = v*10 + uint16(c-'0')
	}
	if v > 255 {
		return 0, ErrInvalidIP
	}
	return uint8(v), nil
}

func parseMaskLen(s string) (int, error) {
	if len(s) == 0 || len(s) > 2 {
		return 0, ErrInvalidMaskLen
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalidMaskLen
	}
	var v int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalidMaskLen
		}
		v = v*10 + int(c-'0')
	}
	if v > 32 {
		return 0, ErrInvalidMaskLen
	}
	return v, nil
}

func maskFor(length int) uint32 {
	if length == 0 {
		return 0
	}
	return ^uint32(0) << (32 - length)
}

type prefix struct {
	network uint32
	length  int
}

func (p prefix) String() string {
	return fmt.Sprintf("%d.%d.%d.%d/%d",
		p.network>>24&0xff, p.network>>16&0xff,
		p.network>>8&0xff, p.network&0xff, p.length)
}

func parseCIDR(s string) (prefix, error) {
	if s == "" || s != strings.TrimSpace(s) {
		return prefix{}, fmt.Errorf("%w: %q", ErrInvalidCIDR, s)
	}
	slash := strings.IndexByte(s, '/')
	if slash < 0 || strings.IndexByte(s[slash+1:], '/') >= 0 {
		return prefix{}, fmt.Errorf("%w: %q", ErrInvalidCIDR, s)
	}
	addr, err := parseIPv4(s[:slash])
	if err != nil {
		return prefix{}, err
	}
	length, err := parseMaskLen(s[slash+1:])
	if err != nil {
		return prefix{}, fmt.Errorf("%w: %q", ErrInvalidMaskLen, s)
	}
	if addr&^maskFor(length) != 0 {
		return prefix{}, fmt.Errorf("%w: %q", ErrHostBitsSet, s)
	}
	return prefix{network: addr, length: length}, nil
}
