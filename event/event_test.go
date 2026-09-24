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
		{"empty payload", Event{Seq: 0, Payload: nil}},
		{"empty non-nil payload", Event{Seq: 1, Payload: []byte{}}},
		{"small", Event{Seq: 42, Payload: []byte("hello")}},
		{"binary", Event{Seq: 1 << 40, Payload: []byte{0, 1, 2, 255}}},
		{"max seq", Event{Seq: ^uint64(0), Payload: []byte("x")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := Encode(tc.ev)
			if len(buf) != EncodedLen(len(tc.ev.Payload)) {
				t.Fatalf("encoded len %d, want %d", len(buf), EncodedLen(len(tc.ev.Payload)))
			}
			got, n, err := Decode(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("consumed %d, want %d", n, len(buf))
			}
			if got.Seq != tc.ev.Seq || !bytes.Equal(got.Payload, tc.ev.Payload) {
				t.Fatalf("round trip got %+v want %+v", got, tc.ev)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	good := Encode(Event{Seq: 7, Payload: []byte("abcd")})
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"empty", nil, ErrTooShort},
		{"short header", good[:HeaderLen-1], ErrTooShort},
		{"truncated payload", good[:len(good)-1], ErrTruncated},
		{"header only", good[:HeaderLen], ErrTruncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode(tc.buf)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}
