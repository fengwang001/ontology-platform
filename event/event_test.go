package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
	name string
	e    Event
	}{
		{"zero", Event{Seq: 0, Payload: nil}},
		{"single", Event{Seq: 1, Payload: []byte("x")}},
		{"empty payload", Event{Seq: 42, Payload: []byte{}}},
		{"binary payload", Event{Seq: -7, Payload: []byte{0, 1, 255, 0, 128}}},
		{"large seq", Event{Seq: 1<<62 + 3, Payload: []byte("hello world")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.e.Encode(nil)
			if len(raw) != tc.e.EncodedSize() {
				t.Fatalf("size = %d, want %d", len(raw), tc.e.EncodedSize())
			}
			got, err := Decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Seq != tc.e.Seq || !bytes.Equal(got.Payload, tc.e.Payload) {
				t.Fatalf("round trip = %+v, want %+v", got, tc.e)
			}
		})
	}
}

func TestDecodeErrorsAndDeterminism(t *testing.T) {
	t.Run("short buffer", func(t *testing.T) {
		for _, n := range []int{0, 1, HeaderSize - 1} {
			if _, err := Decode(make([]byte, n)); !errors.Is(err, ErrShortBuffer) {
				t.Fatalf("n=%d err = %v, want ErrShortBuffer", n, err)
			}
		}
	})
	t.Run("deterministic bytes", func(t *testing.T) {
		e := Event{Seq: 99, Payload: []byte("abc")}
		a, b := e.Encode(nil), e.Encode(nil)
		if !bytes.Equal(a, b) {
			t.Fatal("encode not deterministic")
		}
		if !bytes.Equal(a, []byte{99, 0, 0, 0, 0, 0, 0, 0, 'a', 'b', 'c'}) {
			t.Fatalf("unexpected bytes %v", a)
		}
	})
}
