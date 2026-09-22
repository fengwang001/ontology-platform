package digest

import "testing"

func TestSameContentSameFingerprint(t *testing.T) {
	a := Of([]byte(`{"op":"create","name":"alice"}`))
	b := Of([]byte(`{"op":"create","name":"alice"}`))
	if !a.Equal(b) {
		t.Fatalf("identical bodies must share a fingerprint: %s vs %s", a, b)
	}
	if a != b {
		t.Fatal("fingerprints must be comparable with ==")
	}
}

func TestDifferentContentDifferentFingerprint(t *testing.T) {
	base := OfString("payload-1")
	for _, other := range []string{"payload-2", "payload-1 ", "", "Payload-1"} {
		if base.Equal(OfString(other)) {
			t.Fatalf("different bodies must not share a fingerprint: %q", other)
		}
	}
}

func TestFingerprintDependsOnlyOnContent(t *testing.T) {
	// Two separately allocated slices with the same bytes must match:
	// the fingerprint derives from content, not from identity.
	x := []byte("abc")
	y := append([]byte(nil), x...)
	y[0] = 'a'
	if !Of(x).Equal(Of(y)) {
		t.Fatal("fingerprint must depend on bytes, not slice identity")
	}
}

func TestEmptyBodyHasStableFingerprint(t *testing.T) {
	if !Of(nil).Equal(Of([]byte{})) {
		t.Fatal("nil and empty bodies must share a fingerprint")
	}
	if Of(nil).Equal(OfString("x")) {
		t.Fatal("empty body must differ from non-empty body")
	}
}
