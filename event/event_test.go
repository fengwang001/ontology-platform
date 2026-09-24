package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"empty payload", Event{Seq: 0, Payload: []byte{}}},
		{"nil payload", Event{Seq: 1, Payload: nil}},
		{"small", Event{Seq: 42, Payload: []byte("hello")}},
		{"max seq", Event{Seq: ^uint64(0), Payload: []byte{0x00, 0xff}}},
		{"binary", Event{Seq: 7, Payload: bytes.Repeat([]byte{0xAB}, 300)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(Encode(tc.ev))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Seq != tc.ev.Seq || !bytes.Equal(got.Payload, tc.ev.Payload) {
				t.Fatalf("round trip mismatch: got %+v want %+v", got, tc.ev)
			}
			if len(Encode(tc.ev)) != tc.ev.Size() {
				t.Fatalf("size mismatch")
			}
		})
	}
}

func TestDecodeTruncated(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
	}{
		{"empty", []byte{}},
		{"partial seq", []byte{0, 0, 0}},
		{"one short", make([]byte, HeaderSize-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.buf); !errors.Is(err, ErrEventTruncated) {
				t.Fatalf("want ErrEventTruncated, got %v", err)
			}
		})
	}
}
