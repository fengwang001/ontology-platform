package ontology

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"testing"
)

func testMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func TestSkeleton(t *testing.T) {
	t.Log("macaroon tests ready")
}

func TestMintAttenuateSignatureOrder(t *testing.T) {
	service, err := NewService([]byte("root"), testMAC, 4, 64, 4)
	if err != nil {
		t.Fatal(err)
	}

	token, err := service.Mint([]byte("id"), [][]byte{[]byte("ops:a")}, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Attenuate(service, token, []byte("res:/a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Attenuate(service, first, []byte("amt:3"))
	if err != nil {
		t.Fatal(err)
	}
	swappedOne, err := Attenuate(service, token, []byte("amt:3"))
	if err != nil {
		t.Fatal(err)
	}
	swapped, err := Attenuate(service, swappedOne, []byte("res:/a"))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(second.Sig, swapped.Sig) {
		t.Fatalf("signature did not depend on caveat order: %x", second.Sig)
	}
	if service.MACCalls() != 2+1+1+1+1 {
		t.Fatalf("mac calls = %d", service.MACCalls())
	}
}

func TestInvalidConfiguration(t *testing.T) {
	valid := []struct {
		name  string
		key   []byte
		mac   MACFunc
		caves int
		bytes int
		rev   int
		valid bool
	}{
		{"missing key", nil, testMAC, 1, 1, 1, false},
		{"missing mac", []byte("k"), nil, 1, 1, 1, false},
		{"caveats zero", []byte("k"), testMAC, 0, 1, 1, false},
		{"caveats high", []byte("k"), testMAC, 33, 1, 1, false},
		{"bytes zero", []byte("k"), testMAC, 1, 0, 1, false},
		{"bytes high", []byte("k"), testMAC, 1, 257, 1, false},
		{"rev zero", []byte("k"), testMAC, 1, 1, 0, false},
		{"rev high", []byte("k"), testMAC, 1, 1, 1_000_001, false},
		{"valid", []byte("k"), testMAC, 32, 256, 1_000_000, true},
	}
	for _, item := range valid {
		_, err := NewService(item.key, item.mac, item.caves, item.bytes, item.rev)
		if (err == nil) != item.valid {
			t.Fatalf("%s: err=%v", item.name, err)
		}
	}
}
