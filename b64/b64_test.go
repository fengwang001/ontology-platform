package b64

import (
	"encoding/base64"
	"testing"
)

func TestDecodeGroup(t *testing.T) {
	cases := []struct {
		in   string
		out  string
		n    int
		kind error
		bad  int
	}{
		{"QQ==", "A", 1, nil, -1},
		{"QUI=", "AB", 2, nil, -1},
		{"QUJD", "ABC", 3, nil, -1},
		{"QR==", "", 0, ErrNonCanonical, 1},
		{"QUJ=", "", 0, ErrNonCanonical, 2},
		{"=QUJ", "", 0, ErrBadPadding, 0},
		{"Q=UJ", "", 0, ErrBadPadding, 1},
		{"QU=J", "", 0, ErrBadPadding, 2},
		{"Q!JD", "", 0, ErrInvalidChar, 1},
	}
	for _, c := range cases {
		var g [4]byte
		copy(g[:], c.in)
		var dst [3]byte
		n, bad, err := DecodeGroup(g, dst[:])
		if err != c.kind || bad != c.bad || n != c.n {
			t.Errorf("DecodeGroup(%q) = %d,%d,%v", c.in, n, bad, err)
		}
		if c.kind == nil && string(dst[:n]) != c.out {
			t.Errorf("DecodeGroup(%q) out = %q", c.in, dst[:n])
		}
	}
}

func TestEncodeGroupStdlib(t *testing.T) {
	for n := 0; n <= 64; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*13 + n)
		}
		var got []byte
		for i := 0; i < n; i += 3 {
			g := EncodeGroup(src[i:min(i+3, n)])
			got = append(got, g[:]...)
		}
		if want := base64.StdEncoding.EncodeToString(src); string(got) != want {
			t.Fatalf("n=%d: got %q, want %q", n, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		c  byte
		v  int
		ok bool
	}{
		{'A', 0, true}, {'Z', 25, true}, {'a', 26, true}, {'z', 51, true},
		{'0', 52, true}, {'9', 61, true}, {'+', 62, true}, {'/', 63, true},
		{'=', -1, true}, {' ', 0, false}, {'\t', 0, false}, {'!', 0, false},
	}
	for _, c := range cases {
		if v, ok := Classify(c.c); v != c.v || ok != c.ok {
			t.Errorf("Classify(%q) = %d,%v, want %d,%v", c.c, v, ok, c.v, c.ok)
		}
	}
}
