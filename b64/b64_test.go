package b64

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestGroupRoundTrip(t *testing.T) {
	enc := base64.StdEncoding
	for a := 0; a < 256; a++ {
		in := []byte{byte(a), byte(a >> 3), byte(a >> 5)}
		for n := 1; n <= 3; n++ {
			var want [4]byte
			enc.Encode(want[:], in[:n])
			if got := EncodeGroup(in[:n]); got != want {
				t.Fatalf("EncodeGroup(%x) = %q, want %q", in[:n], got, want)
			}
			out, m, err := DecodeGroup(want)
			if err != nil || !bytes.Equal(out[:m], in[:n]) {
				t.Fatalf("DecodeGroup(%q) = (%x, %v)", want, out[:m], err)
			}
		}
	}
}

func TestDecodeGroupErrors(t *testing.T) {
	cases := []struct {
		in   string
		kind error
		idx  int
	}{
		{"QR==", ErrNonCanonical, 1}, {"QUJ=", ErrNonCanonical, 2},
		{"Q===", ErrPadding, 1}, {"====", ErrPadding, 0},
		{"QQ=Q", ErrPadding, 3}, {"=AAA", ErrPadding, 0},
		{"Q!JD", ErrInvalidChar, 1}, {"AAA ", ErrInvalidChar, 3},
	}
	for _, c := range cases {
		var g [4]byte
		copy(g[:], c.in)
		_, _, err := DecodeGroup(g)
		if !errors.Is(err, c.kind) || err.Idx != c.idx {
			t.Errorf("DecodeGroup(%q): got (%v, %d)", c.in, err, err.Idx)
		}
	}
}
