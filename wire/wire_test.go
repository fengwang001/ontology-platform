package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 1 << 21, 1 << 35, 1 << 63, ^uint64(0)}
	for _, v := range vals {
		buf := AppendVarint(nil, v)
		got, n, err := ReadVarint(buf)
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", v, err)
		}
		if got != v || n != len(buf) {
			t.Fatalf("varint %d: got (%d, %d), want (%d, %d)", v, got, n, v, len(buf))
		}
	}
}

func TestReadVarintOverflow(t *testing.T) {
	buf := bytes.Repeat([]byte{0x80}, 11)
	_, _, err := ReadVarint(buf)
	if !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("want ErrVarintOverflow, got %v", err)
	}
}

func TestReadVarintTruncated(t *testing.T) {
	_, _, err := ReadVarint([]byte{0x80, 0x80})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestParseHeaderVarint(t *testing.T) {
	buf := AppendVarintField(nil, 3, 150)
	f, err := ParseHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if f.Number != 3 || f.Type != Varint || f.Value != 150 {
		t.Fatalf("bad field: %+v", f)
	}
	if f.Total() != len(buf) {
		t.Fatalf("total %d != len %d", f.Total(), len(buf))
	}
}

func TestParseHeaderBytes(t *testing.T) {
	buf := AppendField(nil, 9, Bytes, []byte("hello"))
	f, err := ParseHeader(buf)
	if err != nil {
		t.Fatal(err)
	}
	if f.Number != 9 || f.Type != Bytes || f.Payload != 5 {
		t.Fatalf("bad field: %+v", f)
	}
	if string(buf[f.Header:f.Total()]) != "hello" {
		t.Fatalf("bad payload: %q", buf[f.Header:f.Total()])
	}
}

func TestParseHeaderFieldNumberZero(t *testing.T) {
	_, err := ParseHeader([]byte{0x00, byte(Varint), 0x01})
	assertKind(t, err, KindFieldNumberZero, 0)
}

func TestParseHeaderUnknownWireType(t *testing.T) {
	_, err := ParseHeader([]byte{0x01, 0x07})
	assertKind(t, err, KindUnknownWireType, 1)
}

func TestParseHeaderLengthOverflow(t *testing.T) {
	buf := []byte{0x02, byte(Bytes), 0x05, 0xAA}
	_, err := ParseHeader(buf)
	assertKind(t, err, KindLengthOverflow, 2)
}

func TestParseHeaderTruncated(t *testing.T) {
	_, err := ParseHeader([]byte{0x01})
	assertKind(t, err, KindTruncated, 1)
}

func TestErrorKindsDistinguishable(t *testing.T) {
	sents := []error{
		ErrVarintOverflow, ErrUnknownWireType, ErrLengthOverflow,
		ErrFieldNumberZero, ErrNestedLengthMismatch, ErrTruncated,
	}
	for i, a := range sents {
		for j, b := range sents {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d not distinguishable", i, j)
			}
		}
	}
}

func assertKind(t *testing.T, err error, kind Kind, off int) {
	t.Helper()
	var we *Error
	if !errors.As(err, &we) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if we.Kind != kind || we.Offset != off {
		t.Fatalf("want kind %d offset %d, got kind %d offset %d", kind, off, we.Kind, we.Offset)
	}
	if !errors.Is(err, sentinels[kind]) {
		t.Fatalf("errors.Is mismatch for kind %d", kind)
	}
}
