package pctenc

import (
	"errors"
	"testing"
)

// Semantics 4: hex case-insensitive; '+' stays a literal plus.
func TestDecodeHexCaseAndPlus(t *testing.T) {
	for _, in := range []string{"%2f", "%2F"} {
		got, err := Decode(in, Path)
		if err != nil || got != "/" {
			t.Errorf("Decode(%q) = %q, %v", in, got, err)
		}
	}
	got, err := Decode("a+b", Query)
	if err != nil || got != "a+b" {
		t.Errorf("Decode(a+b) = %q, %v; '+' must stay literal", got, err)
	}
	got, err = Decode("%E4%B8%AD", Query)
	if err != nil || got != "中" {
		t.Errorf("Decode(%%E4%%B8%%AD) = %q, %v", got, err)
	}
}

// Semantics 5: bad escapes yield ErrBadEscape and an empty string.
func TestDecodeBadEscape(t *testing.T) {
	for _, in := range []string{"%", "%A", "%GG", "%2G", "ok%2", "ok%zz"} {
		got, err := Decode(in, Path)
		if !errors.Is(err, ErrBadEscape) {
			t.Errorf("Decode(%q) err = %v, want ErrBadEscape", in, err)
		}
		if got != "" {
			t.Errorf("Decode(%q) = %q, want empty string on error", in, got)
		}
	}
}

// Semantics 7: unencoded safe characters pass through; no content
// whitelist is enforced on decoded bytes.
func TestDecodePassThrough(t *testing.T) {
	got, err := Decode("a:b@c&d", Path)
	if err != nil || got != "a:b@c&d" {
		t.Errorf("Decode pass-through = %q, %v", got, err)
	}
	got, err = Decode("%00%1F", Query)
	if err != nil || got != "\x00\x1f" {
		t.Errorf("Decode control bytes = %q, %v", got, err)
	}
}

// Semantics 8: decode of escape-free input returns it unchanged and is
// deterministic across calls.
func TestDecodeIdentityAndDeterministic(t *testing.T) {
	const s = "plain-Text_1.0~"
	got, err := Decode(s, Fragment)
	if err != nil || got != s {
		t.Errorf("Decode(%q) = %q, %v", s, got, err)
	}
	a, _ := Decode("a%20b", Query)
	b, _ := Decode("a%20b", Query)
	if a != b {
		t.Error("Decode not deterministic")
	}
}
