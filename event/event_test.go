package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Event
	}{
		{"empty payload", Event{Seq: 0, Payload: nil}},
		{"single event", Event{Seq: 1, Payload: []byte("a")}},
		{"large seq", Event{Seq: 1 << 40, Payload: []byte("hello world")}},
		{"binary payload", Event{Seq: 42, Payload: []byte{0, 1, 2, 255, 0, 128}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.in.Encode(nil)
			if len(raw) != tc.in.EncodedLen() {
				t.Fatalf("encoded len = %d, want %d", len(raw), tc.in.EncodedLen())
			}
			got, n, err := Decode(raw)
			if err != nil || n != len(raw) {
				t.Fatalf("decode n=%d err=%v", n, err)
			}
			if got.Seq != tc.in.Seq || !bytes.Equal(got.Payload, tc.in.Payload) {
				t.Fatalf("decoded %+v != %+v", got, tc.in)
			}
		})
	}
}

func TestFrameErrors(t *testing.T) {
	good := Event{Seq: 7, Payload: []byte("payload-bytes")}.Encode(nil)
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"no length prefix", nil, ErrShortLength},
		{"half length prefix", good[:2], ErrShortLength},
		{"cut in payload", good[:FixedPrefix+3], ErrShortFrame},
		{"cut at crc", good[:len(good)-1], ErrShortFrame},
		{"bad crc", flipByte(good, len(good)-1), ErrCRC},
		{"bad payload crc", flipByte(good, FixedPrefix+1), ErrCRC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func flipByte(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i] ^= 0xFF
	return out
}
