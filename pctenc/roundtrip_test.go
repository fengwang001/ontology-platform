package pctenc

import "testing"

// Semantics 6: Decode(Encode(s, m), m) == s for a broad sample of inputs.
func TestRoundTrip(t *testing.T) {
	samples := []string{
		"",
		"plain",
		" \t\n\r\x00\x01\x1f\x7f",
		"!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~",
		"中文字符",
		"héllo wörld",
		"🙂emoji🎉",
		"%25%2F%GG%",
		"a+b=c&d#e",
		string([]byte{0xff, 0xfe, 0x80}),
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~",
	}
	for _, m := range []Mode{Path, Query, Fragment} {
		for _, s := range samples {
			enc := Encode(s, m)
			dec, err := Decode(enc, m)
			if err != nil {
				t.Errorf("Decode(Encode(%q, %d)) err = %v", s, m, err)
				continue
			}
			if dec != s {
				t.Errorf("round trip(%q, %d) = %q", s, m, dec)
			}
		}
	}
}
