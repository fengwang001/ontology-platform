package wire_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"ontology/wire"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 1 << 32, 1<<63 + 5, ^uint64(0)}
	for _, v := range vals {
		buf := wire.AppendVarint(nil, v)
		if len(buf) != wire.VarintLen(v) {
			t.Fatalf("VarintLen(%d)=%d, encoded %d bytes", v, wire.VarintLen(v), len(buf))
		}
		// Cross-check against the standard library encoding.
		var std [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(std[:], v)
		if string(buf) != string(std[:n]) {
			t.Fatalf("encoding of %d differs from encoding/binary", v)
		}
		got, next, err := wire.ReadVarint(buf, 0)
		if err != nil || got != v || next != len(buf) {
			t.Fatalf("ReadVarint(%d) = %d,%d,%v", v, got, next, err)
		}
	}
}

func TestVarintOffset(t *testing.T) {
	buf := []byte{0xAA, 0x96, 0x01} // junk, then varint 150
	v, next, err := wire.ReadVarint(buf, 1)
	if err != nil || v != 150 || next != 3 {
		t.Fatalf("got %d,%d,%v", v, next, err)
	}
}

func TestVarintOverflow(t *testing.T) {
	buf := make([]byte, 11)
	for i := range buf {
		buf[i] = 0x80
	}
	_, _, err := wire.ReadVarint(buf, 0)
	if !errors.Is(err, wire.ErrVarintOverflow) {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
	var we *wire.Error
	if !errors.As(err, &we) || we.Offset != 0 {
		t.Fatalf("want offset 0, got %v", err)
	}
	// 10th byte > 1 also overflows even though it terminates.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := wire.ReadVarint(buf, 0); !errors.Is(err, wire.ErrVarintOverflow) {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
}

func TestVarintTruncated(t *testing.T) {
	_, _, err := wire.ReadVarint([]byte{0x80, 0x80}, 0)
	if !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	buf := wire.AppendHeader(nil, 300, wire.Message)
	h, next, err := wire.ReadHeader(buf, 0)
	if err != nil || h.Field != 300 || h.Type != wire.Message || next != len(buf) {
		t.Fatalf("got %+v,%d,%v", h, next, err)
	}
}

func TestHeaderFieldZero(t *testing.T) {
	_, _, err := wire.ReadHeader([]byte{0x00, 0x00}, 0)
	if !errors.Is(err, wire.ErrFieldNumberZero) {
		t.Fatalf("want ErrFieldNumberZero, got %v", err)
	}
}

func TestHeaderUnknownWireType(t *testing.T) {
	_, _, err := wire.ReadHeader([]byte{0x01, 0x07}, 0)
	if !errors.Is(err, wire.ErrUnknownWireType) {
		t.Fatalf("want ErrUnknownWireType, got %v", err)
	}
	var we *wire.Error
	if !errors.As(err, &we) || we.Offset != 1 {
		t.Fatalf("want offset 1, got %v", err)
	}
}

func TestReadLengthOverflow(t *testing.T) {
	_, _, err := wire.ReadLength([]byte{0x0A, 0x01}, 0)
	if !errors.Is(err, wire.ErrLength) {
		t.Fatalf("want ErrLength, got %v", err)
	}
	n, next, err := wire.ReadLength([]byte{0x02, 0xAA, 0xBB}, 0)
	if err != nil || n != 2 || next != 1 {
		t.Fatalf("got %d,%d,%v", n, next, err)
	}
}

func TestErrorKindsDistinct(t *testing.T) {
	all := []error{
		wire.ErrVarintOverflow, wire.ErrUnknownWireType, wire.ErrLength,
		wire.ErrFieldNumberZero, wire.ErrNestedLengthMismatch, wire.ErrTruncated,
	}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("kinds %d and %d not distinguishable", i, j)
			}
		}
	}
}
