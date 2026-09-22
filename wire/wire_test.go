package wire_test

import (
	"errors"
	"testing"

	"ontology/wire"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 16383, 16384, 1<<32 - 1, 1 << 32, 1<<63 - 1, ^uint64(0)}
	for _, v := range vals {
		b := wire.AppendVarint(nil, v)
		got, n, err := wire.ConsumeVarint(b, 0)
		if err != nil || got != v || n != len(b) {
			t.Fatalf("varint %d: got=%d n=%d err=%v", v, got, n, err)
		}
	}
}

func TestVarintTooLong(t *testing.T) {
	// Eleven bytes all with the continuation bit: never terminates.
	b := make([]byte, 11)
	for i := range b {
		b[i] = 0x80
	}
	_, _, err := wire.ConsumeVarint(b, 0)
	if !errors.Is(err, wire.ErrVarintTooLong) {
		t.Fatalf("want ErrVarintTooLong, got %v", err)
	}
	if pe, ok := err.(*wire.ParseError); !ok || pe.Offset != 0 {
		t.Fatalf("offset: %#v", err)
	}
}

func TestVarintOverflow10thByte(t *testing.T) {
	// Tenth byte may carry at most one bit; 0x80 also continues.
	b := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}
	_, _, err := wire.ConsumeVarint(b, 0)
	if !errors.Is(err, wire.ErrVarintTooLong) {
		t.Fatalf("got %v", err)
	}
}

func TestHeaderUnknownWireType(t *testing.T) {
	// field 1, wire type 9
	b := []byte{0x01, 0x09}
	_, _, err := wire.ConsumeHeader(b, 0, 0)
	if !errors.Is(err, wire.ErrUnknownWireType) {
		t.Fatalf("got %v", err)
	}
	if pe := err.(*wire.ParseError); pe.Offset != 1 {
		t.Fatalf("offset=%d", pe.Offset)
	}
}

func TestHeaderFieldNumberZero(t *testing.T) {
	b := []byte{0x00, 0x00}
	_, _, err := wire.ConsumeHeader(b, 0, 0)
	if !errors.Is(err, wire.ErrFieldNumberZero) {
		t.Fatalf("got %v", err)
	}
	if pe := err.(*wire.ParseError); pe.Offset != 1 {
		t.Fatalf("offset=%d", pe.Offset)
	}
}

func TestHeaderLengthOverflow(t *testing.T) {
	// field 1, bytes, length 10, only 2 payload bytes follow.
	b := []byte{0x01, 0x01, 0x0a, 'a', 'b'}
	_, _, err := wire.ConsumeHeader(b, 0, 0)
	if !errors.Is(err, wire.ErrLengthOverflow) {
		t.Fatalf("got %v", err)
	}
	if pe := err.(*wire.ParseError); pe.Offset != 2 {
		t.Fatalf("offset=%d", pe.Offset)
	}
}

func TestHeaderFieldSizeLimit(t *testing.T) {
	b := []byte{0x01, 0x01, 0x0a, 'a', 'b'}
	_, _, err := wire.ConsumeHeader(b, 0, 2)
	if !errors.Is(err, wire.ErrFieldSizeLimit) {
		t.Fatalf("got %v", err)
	}
}

func TestHeaderVarintPayload(t *testing.T) {
	b := []byte{0x03, 0x00, 0x80, 0x01}
	h, next, err := wire.ConsumeHeader(b, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.Number != 3 || h.Type != wire.Varint || next != 4 {
		t.Fatalf("header=%+v next=%d", h, next)
	}
}
