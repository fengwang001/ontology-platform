package b64

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestEncodeGroupMatchesStdLib(t *testing.T) {
	cases := [][]byte{{0x41}, {0x41, 0x42}, {0x41, 0x42, 0x43}}
	for _, src := range cases {
		got := EncodeGroup(src)
		want := base64.StdEncoding.EncodeToString(src)
		if string(got[:]) != want {
			t.Errorf("EncodeGroup(%v)=%q want %q", src, got, want)
		}
	}
}

func TestDecodeGroup(t *testing.T) {
	cases := []struct {
		name string
		g    string
		want string
		err  error
	}{
		{"one byte canonical", "QQ==", "A", nil},
		{"one byte noncanonical", "QR==", "", ErrNonCanonical},
		{"two byte canonical", "QUI=", "AB", nil},
		{"two byte noncanonical", "QUJ=", "", ErrNonCanonical},
		{"three byte", "QUJD", "ABC", nil},
		{"pad slot zero", "=Q==", "", ErrPadding},
		{"two byte value ok", "QQQ=", "A\x04", nil},
		{"pad slot two not three", "QQ=Q", "", ErrPadding},
		{"illegal char", "Q*==", "", ErrIllegalChar},
		{"newline not a group char", "Q\n==", "", ErrIllegalChar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var g [4]byte
			copy(g[:], tc.g)
			got, err := DecodeGroup(g)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if tc.err == nil && string(got) != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRoundtripGroup(t *testing.T) {
	for n := 1; n <= 3; n++ {
		src := []byte{0x00, 0xFF, 0x0A}[:n]
		g := EncodeGroup(src)
		got, err := DecodeGroup(g)
		if err != nil || string(got) != string(src) {
			t.Fatalf("n=%d roundtrip got %q err=%v", n, got, err)
		}
	}
}
