package enc_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"ontology/enc"
)

// naiveEncodeUint is the handwritten textbook reference: 7-bit groups,
// continuation bit on all but the last group, no canonicality judgment.
func naiveEncodeUint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v&0x7f)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func TestEncodeUintVectors(t *testing.T) {
	cases := []struct {
		v uint64
		b []byte
	}{
		{0, []byte{0x00}}, {1, []byte{0x01}}, {127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}}, {300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}}, {16384, []byte{0x80, 0x80, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
		{1<<64 - 1, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, tc := range cases {
		if got := enc.EncodeUint(tc.v); !bytes.Equal(got, tc.b) {
			t.Errorf("EncodeUint(%d) = % x, want % x", tc.v, got, tc.b)
		}
		if got := enc.EncodeUint(tc.v); !bytes.Equal(got, naiveEncodeUint(tc.v)) {
			t.Errorf("EncodeUint(%d) = % x, naive = % x", tc.v, got, naiveEncodeUint(tc.v))
		}
	}
}

func TestEncodeIntVectors(t *testing.T) {
	cases := []struct {
		v int64
		b []byte
	}{
		{0, []byte{0x00}}, {1, []byte{0x01}}, {-1, []byte{0x7f}},
		{63, []byte{0x3f}}, {-64, []byte{0x40}}, {64, []byte{0xc0, 0x00}},
		{-65, []byte{0xbf, 0x7f}}, {-1 << 63, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x7f}},
	}
	for _, tc := range cases {
		if got := enc.EncodeInt(tc.v); !bytes.Equal(got, tc.b) {
			t.Errorf("EncodeInt(%d) = % x, want % x", tc.v, got, tc.b)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	us := []uint64{0, 1, 127, 128, 1<<32 - 1, 1 << 63, 1<<64 - 1}
	is := []int64{0, 1, -1, 63, 64, -64, -65, 1<<63 - 1, -1 << 63}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2000; i++ {
		us = append(us, rng.Uint64())
		is = append(is, int64(rng.Uint64()))
	}
	for _, v := range us {
		b := enc.EncodeUint(v)
		got, n, err := enc.DecodeUint(b)
		if err != nil || got != v || n != len(b) {
			t.Errorf("uint round-trip %d: got %d n=%d err=%v", v, got, n, err)
		}
	}
	for _, v := range is {
		b := enc.EncodeInt(v)
		got, n, err := enc.DecodeInt(b)
		if err != nil || got != v || n != len(b) {
			t.Errorf("int round-trip %d: got %d n=%d err=%v", v, got, n, err)
		}
	}
}

func TestNonCanonicalRejected(t *testing.T) {
	uintBad := [][]byte{{0x80, 0x00}, {0x80, 0x80, 0x00}, {0xff, 0xff, 0x00}}
	for _, b := range uintBad {
		if _, _, err := enc.DecodeUint(b); !errors.Is(err, enc.ErrNonCanonical) {
			t.Errorf("DecodeUint(% x) err = %v, want ErrNonCanonical", b, err)
		}
	}
	intBad := [][]byte{{0x80, 0x00}, {0xff, 0x7f}, {0x80, 0x80, 0x00}, {0xff, 0xff, 0x7f}}
	for _, b := range intBad {
		if _, _, err := enc.DecodeInt(b); !errors.Is(err, enc.ErrNonCanonical) {
			t.Errorf("DecodeInt(% x) err = %v, want ErrNonCanonical", b, err)
		}
	}
	// Minimal forms of the same values must still be accepted.
	for _, b := range [][]byte{{0x00}, {0x7f}, {0xc0, 0x00}, {0xbf, 0x7f}} {
		if _, _, err := enc.DecodeInt(b); err != nil {
			t.Errorf("DecodeInt(% x) rejected minimal form: %v", b, err)
		}
	}
}

func TestOverflow(t *testing.T) {
	cont := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	if _, _, err := enc.DecodeUint(cont); !errors.Is(err, enc.ErrOverflow) {
		t.Errorf("DecodeUint 10th-byte cont: %v", err)
	}
	if _, _, err := enc.DecodeInt(cont); !errors.Is(err, enc.ErrOverflow) {
		t.Errorf("DecodeInt 10th-byte cont: %v", err)
	}
	big := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := enc.DecodeUint(big); !errors.Is(err, enc.ErrOverflow) {
		t.Errorf("DecodeUint value overflow: %v", err)
	}
	sign := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, _, err := enc.DecodeInt(sign); !errors.Is(err, enc.ErrOverflow) {
		t.Errorf("DecodeInt sign-mismatch 10th byte: %v", err)
	}
}

func TestEmptyInput(t *testing.T) {
	for _, b := range [][]byte{nil, {}, {0x80}, {0xff, 0xff}} {
		if _, _, err := enc.DecodeUint(b); !errors.Is(err, enc.ErrEmptyInput) {
			t.Errorf("DecodeUint(% x) err = %v, want ErrEmptyInput", b, err)
		}
		if _, _, err := enc.DecodeInt(b); !errors.Is(err, enc.ErrEmptyInput) {
			t.Errorf("DecodeInt(% x) err = %v, want ErrEmptyInput", b, err)
		}
	}
	if enc.ErrEmptyInput == enc.ErrOverflow || enc.ErrOverflow == enc.ErrNonCanonical ||
		enc.ErrEmptyInput == enc.ErrNonCanonical {
		t.Fatal("sentinel errors must be mutually distinct")
	}
}
