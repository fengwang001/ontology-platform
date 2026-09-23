package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		e    Event
	}{
		{"zero", Event{0, nil}},
		{"empty payload", Event{7, []byte{}}},
		{"small seq", Event{1, []byte("a")}},
		{"big seq", Event{1 << 40, []byte("hello")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := AppendFrame(nil, tc.e)
			got, err := ReadFrame(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got.Seq != tc.e.Seq || !bytes.Equal(got.Payload, tc.e.Payload) {
				t.Fatalf("got %+v want %+v", got, tc.e)
			}
		})
	}
}

func TestFrameTruncationAndCRC(t *testing.T) {
	raw := AppendFrame(nil, Event{Seq: 42, Payload: []byte("payload")})
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"len prefix", raw[:1], ErrTruncatedBody},
		{"mid body", raw[:len(raw)-3], ErrTruncatedBody},
		{"crc", raw[:len(raw)-1], ErrTruncatedBody},
		{"bad crc", corrupt(raw), ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(tc.data))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func corrupt(b []byte) []byte {
	c := append([]byte(nil), b...)
	c[len(c)-4] ^= 0xFF
	return c
}
