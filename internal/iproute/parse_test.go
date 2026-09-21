package iproute

import (
	"errors"
	"testing"
)

func TestParseIPValid(t *testing.T) {
	cases := map[string]uint32{
		"0.0.0.0":         0,
		"255.255.255.255": 0xffffffff,
		"192.168.1.1":     0xc0a80101,
		"10.0.0.1":        0x0a000001,
		"1.2.3.4":         0x01020304,
	}
	for s, want := range cases {
		got, err := parseIP(s)
		if err != nil {
			t.Fatalf("parseIP(%q) unexpected error: %v", s, err)
		}
		if got != want {
			t.Errorf("parseIP(%q) = %#x, want %#x", s, got, want)
		}
	}
}

func TestParseIPInvalid(t *testing.T) {
	cases := []string{
		"",                 // empty
		"1.2.3",            // too few octets
		"1.2.3.4.5",        // too many octets
		"1.2.3.",           // trailing empty octet
		"1..2.3",           // empty octet in the middle
		"192.168.001.1",    // leading zero
		"010.0.0.1",        // leading zero, classic bypass
		"00.0.0.0",         // leading zero on zero
		"256.0.0.1",        // octet above 255
		"999.1.1.1",        // octet way above 255
		"1.2.3.4 ",         // trailing whitespace
		" 1.2.3.4",         // leading whitespace
		"1.2.3. 4",         // inner whitespace
		"a.b.c.d",          // non-numeric
		"1.2.3.-4",         // sign not allowed
		"1.2.3.+4",         // sign not allowed
		"1.2.3.0x4",        // hex not allowed
		"1234.1.1.1",       // octet too long
		"1.2.3.04",         // leading zero in last octet
		"0xC0.0xA8.0.0x01", // hex octets
		"１２.0.0.1",         // full-width digits
	}
	for _, s := range cases {
		if _, err := parseIP(s); !errors.Is(err, ErrInvalidIP) {
			t.Errorf("parseIP(%q) error = %v, want ErrInvalidIP", s, err)
		}
	}
}

func TestParsePrefixValid(t *testing.T) {
	cases := map[string]prefix{
		"0.0.0.0/0":          {addr: 0, len: 0},
		"10.0.0.0/8":         {addr: 0x0a000000, len: 8},
		"10.1.0.0/16":        {addr: 0x0a010000, len: 16},
		"192.168.1.0/24":     {addr: 0xc0a80100, len: 24},
		"192.168.1.1/32":     {addr: 0xc0a80101, len: 32},
		"255.255.255.255/32": {addr: 0xffffffff, len: 32},
		"172.16.0.0/12":      {addr: 0xac100000, len: 12},
	}
	for s, want := range cases {
		got, err := parsePrefix(s)
		if err != nil {
			t.Fatalf("parsePrefix(%q) unexpected error: %v", s, err)
		}
		if got != want {
			t.Errorf("parsePrefix(%q) = %+v, want %+v", s, got, want)
		}
	}
}

func TestParsePrefixInvalidCIDR(t *testing.T) {
	cases := []string{
		"10.0.0.0",      // missing slash
		"10.0.0.0/",     // empty mask
		"10.0.0.0/8/16", // two slashes
		"10.0.0.0/-1",   // negative
		"10.0.0.0/33",   // above 32
		"10.0.0.0/99",   // way above 32
		"10.0.0.0/08",   // leading zero in mask
		"10.0.0.0/00",   // leading zero on zero-length mask
		"10.0.0.0/abc",  // non-numeric mask
		"10.0.0.0/8 ",   // trailing whitespace
		"10.0.0.0/ 8",   // whitespace after slash
		"10.0.0.0/128",  // three-digit mask
	}
	for _, s := range cases {
		if _, err := parsePrefix(s); !errors.Is(err, ErrInvalidCIDR) {
			t.Errorf("parsePrefix(%q) error = %v, want ErrInvalidCIDR", s, err)
		}
	}
}

func TestParsePrefixHostBits(t *testing.T) {
	cases := []string{
		"192.168.1.1/24", // host bits set
		"10.1.2.3/8",     // host bits set
		"0.0.0.1/0",      // host bits set on default
		"255.255.255.255/31",
		"192.168.1.128/25", // valid actually: 128 in last octet with /25 is network
	}
	for _, s := range cases[:4] {
		if _, err := parsePrefix(s); !errors.Is(err, ErrHostBitsSet) {
			t.Errorf("parsePrefix(%q) error = %v, want ErrHostBitsSet", s, err)
		}
	}
	// The last case is a valid network and must parse cleanly.
	if _, err := parsePrefix(cases[4]); err != nil {
		t.Errorf("parsePrefix(%q) unexpected error: %v", cases[4], err)
	}
}

func TestParsePrefixInvalidIPPart(t *testing.T) {
	// Bad IP portion of a CIDR surfaces as ErrInvalidIP, not ErrInvalidCIDR.
	for _, s := range []string{"10.0.0.0.1/8", "010.0.0.0/8", "1.2.3/24", "10.0.0.0 /8", "/8"} {
		if _, err := parsePrefix(s); !errors.Is(err, ErrInvalidIP) {
			t.Errorf("parsePrefix(%q) error = %v, want ErrInvalidIP", s, err)
		}
	}
}
