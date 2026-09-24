package event

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty", []byte{}},
		{"one byte", []byte{0xAB}},
		{"text", []byte("hello world")},
		{"binary", bytes.Repeat([]byte{0, 1, 2, 255}, 300)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := Encode(tc.payload)
			if len(buf) != EncodedSize(len(tc.payload)) {
				t.Fatalf("encoded size %d, want %d", len(buf), EncodedSize(len(tc.payload)))
			}
			got, n, err := Decode(bytes.NewReader(buf))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("consumed %d bytes, want %d", n, len(buf))
			}
			if !bytes.Equal(got, tc.payload) {
				t.Fatalf("payload mismatch: got %v want %v", got, tc.payload)
			}
		})
	}
}

func TestTruncationClasses(t *testing.T) {
	payload := []byte("12345678")
	full := Encode(payload)
	for cut := 1; cut < len(full); cut++ {
		_, _, err := Decode(bytes.NewReader(full[:cut]))
		var want error
		switch {
		case cut < LenSize:
			want = ErrShortLengthPrefix
		case cut < LenSize+len(payload):
			want = ErrShortBody
		default:
			want = ErrCRCMismatch
		}
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d: got %v, want %v", cut, err, want)
		}
	}
	if _, _, err := Decode(bytes.NewReader(nil)); err != io.EOF {
		t.Fatalf("empty input: got %v, want io.EOF", err)
	}
}

func TestCorruption(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"flipped payload byte", func(b []byte) { b[LenSize] ^= 0xFF }, ErrCRCMismatch},
		{"flipped crc byte", func(b []byte) { b[len(b)-1] ^= 0xFF }, ErrCRCMismatch},
		{"oversized length", func(b []byte) { b[0] = 0xFF; b[1] = 0xFF }, ErrBadLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := Encode([]byte("payload"))
			tc.mutate(buf)
			if _, _, err := Decode(bytes.NewReader(buf)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
