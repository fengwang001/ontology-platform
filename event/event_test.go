package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		in   Event
	}{
		{"zero", Event{Seq: 0, Payload: nil}},
		{"empty payload", Event{Seq: 1, Payload: []byte{}}},
		{"normal", Event{Seq: 42, Payload: []byte("hello")}},
		{"binary", Event{Seq: 1<<64 - 1, Payload: []byte{0, 255, 1, 254}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.in.Encode(nil)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if len(raw) != tc.in.EncodedLen() {
				t.Fatalf("encoded len = %d, want %d", len(raw), tc.in.EncodedLen())
			}
			out, n, err := Decode(raw)
			if err != nil || n != len(raw) {
				t.Fatalf("decode: n=%d err=%v", n, err)
			}
			if out.Seq != tc.in.Seq || !bytes.Equal(out.Payload, tc.in.Payload) {
				t.Fatalf("round trip = %+v, want %+v", out, tc.in)
			}
			if len(tc.in.Payload) == 0 && out.Payload == nil {
				t.Fatalf("empty payload must remain non-nil-safe: got nil")
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	good, _ := Event{Seq: 7, Payload: []byte("abc")}.Encode(nil)
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"short header", good[:HeaderLen-1], ErrShortRecord},
		{"short body", good[:HeaderLen+2], ErrShortRecord},
		{"short crc", good[:len(good)-1], ErrShortRecord},
		{"bad crc", flipByte(good, len(good)-1), ErrCRC},
		{"bad body crc", flipByte(good, HeaderLen), ErrCRC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Adjacent records decode independently with exact consumption.
	var multi []byte
	multi, _ = Event{Seq: 1, Payload: []byte("x")}.Encode(multi)
	multi, _ = Event{Seq: 2, Payload: nil}.Encode(multi)
	e1, n1, err := Decode(multi)
	if err != nil || e1.Seq != 1 {
		t.Fatalf("first = %v %v", e1, err)
	}
	e2, n2, err := Decode(multi[n1:])
	if err != nil || e2.Seq != 2 || n1+n2 != len(multi) {
		t.Fatalf("second = %v %v total=%d/%d", e2, err, n1+n2, len(multi))
	}
}

func flipByte(b []byte, i int) []byte {
	c := bytes.Clone(b)
	c[i] ^= 0xFF
	return c
}
