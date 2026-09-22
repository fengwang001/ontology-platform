package digest

import "testing"

func TestSameContentSameFingerprint(t *testing.T) {
	a := Of([]byte(`{"op":"create","name":"x"}`))
	b := Of([]byte(`{"op":"create","name":"x"}`))
	if !a.Equal(b) {
		t.Fatalf("identical bodies must share fingerprint: %s vs %s", a, b)
	}
}

func TestDifferentContentDifferentFingerprint(t *testing.T) {
	cases := [][2]string{
		{`{"a":1}`, `{"a":2}`},
		{`{"a":1}`, `{"a":1,"b":null}`},
		{"", " "},
		{"abc", "abd"},
	}
	for _, c := range cases {
		if Of([]byte(c[0])).Equal(Of([]byte(c[1]))) {
			t.Fatalf("different bodies %q and %q share fingerprint", c[0], c[1])
		}
	}
}

func TestFingerprintIsComparableAndStable(t *testing.T) {
	f := Of([]byte("payload"))
	m := map[Fingerprint]int{f: 1}
	if m[Of([]byte("payload"))] != 1 {
		t.Fatal("fingerprint must be usable as a comparable map key")
	}
	if len(f.String()) != 64 {
		t.Fatalf("hex string length = %d, want 64", len(f.String()))
	}
}
