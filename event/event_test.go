package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		pl   []byte
	}{
		{"empty", []byte{}},
		{"one", []byte{0x7f}},
		{"bytes", []byte("append-only event log")},
		{"zero-nul", []byte{0, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := EncodeFrame(nil, tc.pl)
			if len(frame) != FrameLen(tc.pl) {
				t.Fatalf("frame len = %d, want %d", len(frame), FrameLen(tc.pl))
			}
			n, err := DecodeFrameAt(frame)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(tc.pl) || !bytes.Equal(Payload(frame), tc.pl) {
				t.Fatalf("payload mismatch: %q vs %q", Payload(frame), tc.pl)
			}
		})
	}
}

func TestFrameTruncationAndCorruption(t *testing.T) {
	full := EncodeFrame(nil, []byte("hello-crc"))
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"no-length", full[:2], ErrLength},
		{"body-short", full[:LenSize+2], ErrBody},
		{"crc-short", full[:len(full)-2], ErrCRC},
		{"payload-bitflip", flip(full, LenSize+1), ErrCRC},
		{"crc-bitflip", flip(full, len(full)-1), ErrCRC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeFrameAt(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func flip(b []byte, i int) []byte {
	out := bytes.Clone(b)
	out[i] ^= 0xff
	return out
}
