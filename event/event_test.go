package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		seq     uint64
		payload []byte
	}{
		{"empty payload", 0, nil},
		{"zero seq with payload", 0, []byte("hello")},
		{"max seq", ^uint64(0), []byte{0x00, 0xff}},
		{"binary payload", 42, []byte{0, 1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(Encode(Event{Seq: tc.seq, Payload: tc.payload}))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Seq != tc.seq || !bytes.Equal(got.Payload, tc.payload) {
				t.Fatalf("round trip = %+v, want seq=%d payload=%x", got, tc.seq, tc.payload)
			}
			if len(got.Payload) == 0 && got.Payload == nil && tc.payload != nil && len(tc.payload) == 0 {
				t.Fatalf("payload normalization mismatch")
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
	}{
		{"empty", []byte{}},
		{"one byte", []byte{1}},
		{"seven bytes", make([]byte, 7)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.buf); !errors.Is(err, ErrTooShort) {
				t.Fatalf("Decode err = %v, want ErrTooShort", err)
			}
		})
	}
}
