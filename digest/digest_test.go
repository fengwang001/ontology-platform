package digest

import "testing"

func TestSameContentSameFingerprint(t *testing.T) {
	a := Of([]byte(`{"name":"alice","age":30}`))
	b := Of([]byte(`{"name":"alice","age":30}`))
	if a != b {
		t.Fatalf("same content must yield same fingerprint: %v vs %v", a, b)
	}
	if !a.Equal(b) {
		t.Fatal("Equal must report true for identical contents")
	}
}

func TestDifferentContentDifferentFingerprint(t *testing.T) {
	a := Of([]byte(`{"name":"alice"}`))
	b := Of([]byte(`{"name":"bob"}`))
	if a == b {
		t.Fatal("different content must yield different fingerprints")
	}
	if a.Equal(b) {
		t.Fatal("Equal must report false for different contents")
	}
}

func TestFingerprintIsContentBasedNotIdentityBased(t *testing.T) {
	x := []byte("payload")
	y := make([]byte, len(x))
	copy(y, x)
	if Of(x) != Of(y) {
		t.Fatal("distinct slices with equal content must share a fingerprint")
	}
	y[0] = 'X'
	if Of(x) == Of(y) {
		t.Fatal("mutated content must change the fingerprint")
	}
}

func TestEmptyBody(t *testing.T) {
	if Of(nil) != Of([]byte{}) {
		t.Fatal("nil and empty body must share a fingerprint")
	}
}

func TestStringIsHex(t *testing.T) {
	s := Of([]byte("x")).String()
	if len(s) != 64 {
		t.Fatalf("hex fingerprint must be 64 chars, got %d", len(s))
	}
}
