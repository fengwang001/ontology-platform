package event

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"empty payload", Event{Seq: 1, Payload: nil}},
		{"empty bytes", Event{Seq: 2, Payload: []byte{}}},
		{"normal", Event{Seq: 42, Payload: []byte("hello")}},
		{"max seq", Event{Seq: ^uint64(0), Payload: []byte{0, 1, 2, 3, 255}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.ev.Size())
			if err := tc.ev.Encode(buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, n, err := Decode(buf)
			if err != nil || n != tc.ev.Size() {
				t.Fatalf("decode: err=%v n=%d want=%d", err, n, tc.ev.Size())
			}
			if got.Seq != tc.ev.Seq || !bytes.Equal(got.Payload, tc.ev.Payload) {
				t.Fatalf("round trip=%+v want=%+v", got, tc.ev)
			}
		})
	}
}

func TestErrorsAndAppend(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		err  error
	}{
		{"nil", nil, ErrShortBody},
		{"seven bytes", []byte{1, 2, 3, 4, 5, 6, 7}, ErrShortBody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode(tc.src); !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
		})
	}
	ev := Event{Seq: 9, Payload: []byte("ab")}
	if got := ev.AppendTo(nil); len(got) != ev.Size() {
		t.Fatalf("append len=%d want=%d", len(got), ev.Size())
	}
	small := make([]byte, ev.Size()-1)
	if err := ev.Encode(small); err == nil {
		t.Fatal("expected dst-too-small error")
	}
}
