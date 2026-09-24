package b64

import "testing"

func TestDecodeGroup(t *testing.T) {
	cases := []struct {
		g    string
		n    int
		pos  int
		k    Kind
		want []byte
	}{
		{"QQ==", 1, 0, OK, []byte("A")},
		{"QR==", 0, 1, NonCanonical, nil},
		{"QUI=", 2, 0, OK, []byte("AB")},
		{"QUJ=", 0, 2, NonCanonical, nil},
		{"QUJD", 3, 0, OK, []byte("ABC")},
		{"////", 3, 0, OK, []byte{0xFF, 0xFF, 0xFF}},
		{"=Q==", 0, 0, BadPad, nil},
		{"Q=Q=", 0, 1, BadPad, nil},
		{"Q===", 0, 1, BadPad, nil},
		{"====", 0, 0, BadPad, nil},
		{"Q!JD", 0, 1, BadChar, nil},
		{"QUJ\n", 0, 3, BadChar, nil},
	}
	for _, c := range cases {
		var g [4]byte
		copy(g[:], c.g)
		var o [3]byte
		n, pos, k := DecodeGroup(g, &o)
		if n != c.n || pos != c.pos || k != c.k {
			t.Errorf("%q: got (%d,%d,%d), want (%d,%d,%d)", c.g, n, pos, k, c.n, c.pos, c.k)
		}
		if c.want != nil && string(o[:n]) != string(c.want) {
			t.Errorf("%q: got %q, want %q", c.g, o[:n], c.want)
		}
	}
}

func TestEncodeGroup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"A", "QQ=="},
		{"AB", "QUI="},
		{"ABC", "QUJD"},
		{"\xff", "/w=="},
		{"\xff\xff", "//8="},
		{"\xff\xff\xff", "////"},
	}
	for _, c := range cases {
		if got := string(EncodeGroup(nil, []byte(c.in)...)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}
