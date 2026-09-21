package iproute

import (
	"errors"
	"testing"
)

func TestParseIPv4Valid(t *testing.T) {
	cases := map[string]uint32{
		"0.0.0.0":         0,
		"255.255.255.255": 0xffffffff,
		"192.168.1.1":     0xc0a80101,
		"10.0.0.1":        0x0a000001,
		"1.2.3.4":         0x01020304,
	}
	for in, want := range cases {
		got, err := parseIPv4(in)
		if err != nil {
			t.Errorf("parseIPv4(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseIPv4(%q) = %#x, want %#x", in, got, want)
		}
	}
}

func TestParseIPv4Invalid(t *testing.T) {
	bad := []string{
		"",              // empty
		"192.168.001.1", // leading zero
		"010.0.0.1",     // leading zero
		"00.1.2.3",      // leading zero
		"1.2.3",         // too few octets
		"1.2.3.4.5",     // too many octets
		"1..2.3",        // empty octet
		"1.2.3.",        // trailing empty octet
		".1.2.3",        // leading empty octet
		"256.1.1.1",     // octet > 255
		"1.2.3.999",     // octet > 255
		"a.b.c.d",       // non-digit
		"1.2.3.4a",      // trailing junk
		"1.2.3.-4",      // sign
		" 1.2.3.4",      // leading space
		"1.2.3.4 ",      // trailing space
		"1. 2.3.4",      // inner space
		"0x1.2.3.4",     // hex-ish
		"1.2.3.04",      // leading zero, last octet
		"1234.1.1.1",    // too many digits
		"１２.3.4.5",      // full-width digits
	}
	for _, in := range bad {
		if _, err := parseIPv4(in); !errors.Is(err, ErrInvalidIP) {
			t.Errorf("parseIPv4(%q): want ErrInvalidIP, got %v", in, err)
		}
	}
}

func TestParseCIDRValid(t *testing.T) {
	cases := []struct {
		in   string
		addr uint32
		bits int
	}{
		{"0.0.0.0/0", 0, 0},
		{"10.0.0.0/8", 0x0a000000, 8},
		{"10.1.0.0/16", 0x0a010000, 16},
		{"192.168.1.0/24", 0xc0a80100, 24},
		{"192.168.1.1/32", 0xc0a80101, 32},
		{"255.255.255.255/32", 0xffffffff, 32},
		{"1.2.3.0/31", 0x01020300, 31},
	}
	for _, c := range cases {
		p, err := parseCIDR(c.in)
		if err != nil {
			t.Errorf("parseCIDR(%q) unexpected error: %v", c.in, err)
			continue
		}
		if p.addr != c.addr || p.bits != c.bits {
			t.Errorf("parseCIDR(%q) = {%#x /%d}, want {%#x /%d}",
				c.in, p.addr, p.bits, c.addr, c.bits)
		}
		if p.canonical() != c.in {
			t.Errorf("canonical of %q = %q", c.in, p.canonical())
		}
	}
}

func TestParseCIDRInvalidMask(t *testing.T) {
	bad := []string{
		"10.0.0.0/",    // empty length
		"10.0.0.0/33",  // > 32
		"10.0.0.0/08",  // leading zero
		"10.0.0.0/00",  // leading zero
		"10.0.0.0/-1",  // sign
		"10.0.0.0/8x",  // junk
		"10.0.0.0/ 8",  // space
		"10.0.0.0/100", // too long / out of range
	}
	for _, in := range bad {
		if _, err := parseCIDR(in); !errors.Is(err, ErrInvalidMask) {
			t.Errorf("parseCIDR(%q): want ErrInvalidMask, got %v", in, err)
		}
	}
}

func TestParseCIDRHostBitsSet(t *testing.T) {
	bad := []string{
		"192.168.1.1/24",
		"10.1.2.3/8",
		"0.0.0.1/0",
		"255.255.255.255/0",
		"1.2.3.5/31",
	}
	for _, in := range bad {
		if _, err := parseCIDR(in); !errors.Is(err, ErrHostBitsSet) {
			t.Errorf("parseCIDR(%q): want ErrHostBitsSet, got %v", in, err)
		}
	}
}

func TestParseCIDRMalformed(t *testing.T) {
	bad := []string{
		"10.0.0.0",     // missing slash
		"10.0.0.0/8/1", // double slash
		"/8",           // missing address
		"10.0.0.0 /8",  // space before slash
		"10.0.0.0.0/8", // bad address part
		"10.0.0.256/8", // bad octet
		"10.0.0.08/8",  // leading zero in address
	}
	for _, in := range bad {
		if _, err := parseCIDR(in); err == nil {
			t.Errorf("parseCIDR(%q): want error, got nil", in)
		}
	}
}
