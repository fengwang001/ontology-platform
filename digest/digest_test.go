package digest

import "testing"

func TestSameBodySameDigest(t *testing.T) {
	a := Of([]byte(`{"op":"create","name":"alice"}`))
	b := Of([]byte(`{"op":"create","name":"alice"}`))
	if !a.Equal(b) {
		t.Fatalf("identical bodies must share a fingerprint: %s vs %s", a, b)
	}
}

func TestDifferentBodiesDifferentDigest(t *testing.T) {
	cases := [][2]string{
		{`{"name":"alice"}`, `{"name":"bob"}`},
		{`{"name":"alice"}`, `{"name":"alice "}`},
		{"", "\x00"},
		{"abc", "abd"},
	}
	for _, c := range cases {
		if Of([]byte(c[0])).Equal(Of([]byte(c[1]))) {
			t.Fatalf("different bodies %q and %q share a fingerprint", c[0], c[1])
		}
	}
}

func TestEmptyBodyStable(t *testing.T) {
	if !Of(nil).Equal(Of([]byte{})) {
		t.Fatal("nil and empty body must have the same fingerprint")
	}
}

func TestStringIsHex(t *testing.T) {
	s := Of([]byte("x")).String()
	if len(s) != 64 {
		t.Fatalf("hex fingerprint must be 64 chars, got %d", len(s))
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("non-hex char %q in %q", r, s)
		}
	}
}
