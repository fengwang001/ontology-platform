package pctenc

import "testing"

// Semantics 1: unreserved characters are never encoded, in any mode.
func TestEncodeUnreservedNeverEscaped(t *testing.T) {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	for _, m := range []Mode{Path, Query, Fragment} {
		if got := Encode(unreserved, m); got != unreserved {
			t.Errorf("Encode(unreserved, %d) = %q", m, got)
		}
	}
}

// Semantics 2: per-mode safe sets; query space is %20, not '+'.
func TestEncodeModeSafeSets(t *testing.T) {
	cases := []struct {
		in   string
		mode Mode
		want string
	}{
		{":@&=+$,", Path, ":@&=+$,"},
		{"/?", Path, "%2F%3F"},
		{"&=+#", Query, "%26%3D%2B%23"},
		{"a b", Query, "a%20b"},
		{"/?", Fragment, "/?"},
		{"&=+", Fragment, "%26%3D%2B"},
		{"#", Fragment, "%23"},
		{"\x00\x1f\x7f", Path, "%00%1F%7F"},
	}
	for _, c := range cases {
		if got := Encode(c.in, c.mode); got != c.want {
			t.Errorf("Encode(%q, %d) = %q, want %q", c.in, c.mode, got, c.want)
		}
	}
}

// Semantics 3: uppercase hex, per-byte UTF-8.
func TestEncodeUpperHexUTF8(t *testing.T) {
	if got := Encode("中", Query); got != "%E4%B8%AD" {
		t.Errorf("Encode(中) = %q", got)
	}
	if got := Encode("/", Path); got != "%2F" {
		t.Errorf("Encode(/) = %q, want uppercase %%2F", got)
	}
}

// Semantics 8: encode of safe input returns the same string, and is
// deterministic across calls.
func TestEncodeIdentityAndDeterministic(t *testing.T) {
	const s = "abc-_.~XYZ09"
	if got := Encode(s, Query); got != s {
		t.Errorf("Encode safe input = %q", got)
	}
	mixed := "a b/中:@"
	if Encode(mixed, Path) != Encode(mixed, Path) {
		t.Error("Encode not deterministic")
	}
}
