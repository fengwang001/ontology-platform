package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeConsume(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"empty", []byte{}},
		{"one", []byte{0x01}},
		{"text", []byte("hello world")},
		{"binary", []byte{0, 255, 1, 254, 128, 0, 0, 9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Encode(nil, tc.body)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if len(raw) != EncodedLen(tc.body) {
				t.Fatalf("len = %d, want %d", len(raw), EncodedLen(tc.body))
			}
			got, n, err := Consume(raw)
			if err != nil || n != len(raw) || !bytes.Equal(got, tc.body) {
				t.Fatalf("consume = %q,%d,%v", got, n, err)
			}
			if len(tc.body) > 0 {
				mut := append([]byte(nil), got...)
				mut[0] ^= 0xFF
				if bytes.Equal(raw[4:4+len(tc.body)], mut) {
					t.Fatal("payload shares storage with frame")
				}
			}
		})
	}
}

func TestConsumeErrors(t *testing.T) {
	raw, _ := Encode(nil, []byte("abc"))
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"short-length", raw[:3], ErrShortLength},
		{"short-body", raw[:5], ErrShortFrame},
		{"short-crc", raw[:len(raw)-1], ErrShortFrame},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Consume(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCRCMismatch(t *testing.T) {
	raw, _ := Encode(nil, []byte("abc"))
	raw[5] ^= 0xFF
	if _, _, err := Consume(raw); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("err = %v, want ErrCRCMismatch", err)
	}
}
