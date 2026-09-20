package pctenc

import (
	"errors"
	"testing"
)

func TestDecodeHexCaseInsensitive(t *testing.T) {
	for _, m := range []Mode{Path, Query, Fragment} {
		lo, errLo := Decode("%2f", m)
		up, errUp := Decode("%2F", m)
		if errLo != nil || errUp != nil || lo != "/" || up != "/" {
			t.Fatalf("Decode(%%2f/%%2F, %v) = %q,%v %q,%v", m, lo, errLo, up, errUp)
		}

		if got, err := Decode("%e4%b8%ad", m); err != nil || got != "中" {
			t.Errorf("Decode lowercase UTF-8, %v = %q, %v", m, got, err)
		}
	}
}

func TestDecodePlusIsLiteral(t *testing.T) {
	for _, m := range []Mode{Path, Query, Fragment} {
		if got, err := Decode("a+b", m); err != nil || got != "a+b" {
			t.Errorf("Decode(\"a+b\", %v) = %q, %v; want literal plus", m, got, err)
		}
	}
}

func TestDecodeSafeRawBytesPassThrough(t *testing.T) {
	if got, err := Decode("a:b@c&d=e", Path); err != nil || got != "a:b@c&d=e" {
		t.Errorf("Decode raw path-safe = %q, %v", got, err)
	}
	if got, err := Decode("a/b?c", Fragment); err != nil || got != "a/b?c" {
		t.Errorf("Decode raw fragment-safe = %q, %v", got, err)
	}
	// Decoding cares only about escape syntax: an unencoded '&' in query
	// mode is still just a byte and must pass through.
	if got, err := Decode("a&b=c", Query); err != nil || got != "a&b=c" {
		t.Errorf("Decode raw bytes in query mode = %q, %v", got, err)
	}
}

func TestDecodeBadEscapes(t *testing.T) {
	bad := []string{"%", "%A", "%GG", "%2G", "abc%", "ab%A", "%GGabc", "a%2G"}
	for _, in := range bad {
		got, err := Decode(in, Query)
		if !errors.Is(err, ErrBadEscape) {
			t.Errorf("Decode(%q) err = %v, want ErrBadEscape", in, err)
		}
		if got != "" {
			t.Errorf("Decode(%q) = %q, want empty string on error", in, got)
		}
	}
}

func TestDecodeEmpty(t *testing.T) {
	for _, m := range []Mode{Path, Query, Fragment} {
		got, err := Decode("", m)
		if err != nil || got != "" {
			t.Errorf("Decode(\"\", %v) = %q, %v", m, got, err)
		}
	}
}
