package event

import (
	"errors"
	"testing"
)

func TestEncodeValidate(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"normal", []byte("hello-event")},
		{"empty", []byte{}},
		{"binary", []byte{0, 1, 255, 0, 128}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := EncodeFrame(nil, tc.payload)
			if got := FrameLen(len(tc.payload)); got != len(frame) {
				t.Fatalf("FrameLen=%d want %d", got, len(frame))
			}
			n, err := ValidateFrame(frame)
			if err != nil || n != len(tc.payload) {
				t.Fatalf("ValidateFrame n=%d err=%v", n, err)
			}
		})
	}
}

func TestCorruptFrame(t *testing.T) {
	cases := []struct {
		name string
		mut  func([]byte)
		want error
	}{
		{"flip payload", func(b []byte) { b[6] ^= 0xFF }, ErrCRCMismatch},
		{"flip crc", func(b []byte) { b[len(b)-1] ^= 0x01 }, ErrCRCMismatch},
		{"flip length", func(b []byte) { b[0] ^= 0x01 }, ErrFrameTruncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := EncodeFrame(nil, []byte("abcdefgh"))
			tc.mut(frame)
			if _, err := ValidateFrame(frame); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}
