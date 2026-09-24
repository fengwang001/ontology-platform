package b64

import (
	"errors"
	"testing"
)

func TestEncodeGroup(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"one", []byte{0}, "AA=="},
		{"A", []byte("A"), "QQ=="},
		{"two", []byte("AB"), "QUI="},
		{"three", []byte("ABC"), "QUJD"},
		{"allbits", []byte{0xFB, 0xFF, 0xBF}, "+/+/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var g [4]byte
			EncodeGroup(g[:], tc.in)
			if string(g[:]) != tc.want {
				t.Fatalf("got %q want %q", g, tc.want)
			}
		})
	}
}

func TestDecodeGroup(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		cause error
		atIdx int
	}{
		{"canonical one", "QQ==", "A", nil, 0},
		{"noncanonical one", "QR==", "", ErrCanonical, 1},
		{"canonical two", "QUI=", "AB", nil, 0},
		{"noncanonical two", "QUJ=", "", ErrCanonical, 2},
		{"full", "QUJD", "ABC", nil, 0},
		{"allbits", "+/+/", "\xfb\xff\xbf", nil, 0},
		{"illegal zero", "*Q==", "", ErrIllegalChar, 0},
		{"illegal three", "QQ=*", "", ErrIllegalChar, 3},
		{"eq at zero", "=Q==", "", ErrPadding, 0},
		{"eq at one", "Q===", "", ErrPadding, 1},
		{"eq at two full", "QQ=Q", "", ErrPadding, 2},
		{"space", "Q Q=", "", ErrIllegalChar, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var g [4]byte
			copy(g[:], tc.in)
			got, err := DecodeGroup(g)
			if tc.cause == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if string(got) != tc.want {
					t.Fatalf("got %q want %q", got, tc.want)
				}
				return
			}
			var ge *GroupError
			if !errors.As(err, &ge) || !errors.Is(err, tc.cause) || ge.Index != tc.atIdx {
				t.Fatalf("got %v (cause=%v idx=%v), want %v at %d", err, ge, ge.Index, tc.cause, tc.atIdx)
			}
		})
	}
}

func TestGroupRoundTrip(t *testing.T) {
	cases := [][]byte{{0}, {255}, {0, 1}, {254, 255}, {0, 1, 2}, {255, 254, 253}}
	for _, src := range cases {
		var g [4]byte
		EncodeGroup(g[:], src)
		got, err := DecodeGroup(g)
		if err != nil || string(got) != string(src) {
			t.Fatalf("roundtrip %v: got %q err=%v", src, got, err)
		}
	}
}
