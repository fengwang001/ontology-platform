package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrames(t *testing.T) {
	cases := []struct {
		name    string
		ev      Event
		wantErr error
	}{
		{"normal", Event{Seq: 7, Payload: []byte("hello")}, nil},
		{"empty payload", Event{Seq: 1, Payload: []byte{}}, nil},
		{"binary payload", Event{Seq: 2, Payload: []byte{0, 1, 255, 0, 13}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.ev.Encode(nil)
			if len(raw) != tc.ev.EncodedLen() {
				t.Fatalf("encoded len = %d, want %d", len(raw), tc.ev.EncodedLen())
			}
			got, end, err := DecodeAt(raw, 0)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && (!bytes.Equal(got.Payload, tc.ev.Payload) || end != len(raw)) {
				t.Fatalf("decode = %+v end=%d, want payload %v end=%d", got, end, tc.ev.Payload, len(raw))
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	raw := Event{Seq: 1, Payload: []byte("abcdef")}.Encode(nil)
	cases := []struct {
		name    string
		data    []byte
		off     int
		wantErr error
	}{
		{"empty", nil, 0, ErrShortFrame},
		{"three bytes", raw[:3], 0, ErrShortFrame},
		{"body cut", raw[:len(raw)-3], 0, ErrShortFrame},
		{"crc cut", raw[:len(raw)-1], 0, ErrShortFrame},
		{"offset past end", raw, len(raw), ErrShortFrame},
		{"flipped crc", flipByte(raw, len(raw)-1), 0, ErrCRCMismatch},
		{"flipped payload", flipByte(raw, LenSize), 0, ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := DecodeAt(tc.data, tc.off); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func flipByte(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= 0xFF
	return out
}
