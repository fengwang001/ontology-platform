package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", []byte{}},
		{"nil payload", nil},
		{"small", []byte("hello")},
		{"binary", []byte{0x00, 0xff, 0x01, 0xfe}},
		{"64KiB", bytes.Repeat([]byte("x"), 1<<16)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := Encode(tc.payload)
			if got := len(rec); got != EncodedSize(len(tc.payload)) {
				t.Fatalf("encoded size = %d, want %d", got, EncodedSize(len(tc.payload)))
			}
			payload, n, err := Decode(rec)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(rec) {
				t.Fatalf("consumed = %d, want %d", n, len(rec))
			}
			if !bytes.Equal(payload, tc.payload) {
				t.Fatalf("payload mismatch")
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	rec := Encode([]byte("hello")) // 4 + 5 + 4 = 13 bytes
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"zero bytes", rec[:0], ErrLengthIncomplete},
		{"1 byte of len", rec[:1], ErrLengthIncomplete},
		{"3 bytes of len", rec[:3], ErrLengthIncomplete},
		{"len only", rec[:4], ErrBodyIncomplete},
		{"partial payload", rec[:7], ErrBodyIncomplete},
		{"full payload no crc", rec[:9], ErrCRCMismatch},
		{"partial crc", rec[:11], ErrCRCMismatch},
		{"corrupted payload", flip(rec, 5), ErrCRCMismatch},
		{"corrupted crc", flip(rec, 12), ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode(tc.buf)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func flip(rec []byte, i int) []byte {
	out := make([]byte, len(rec))
	copy(out, rec)
	out[i] ^= 0xff
	return out
}
