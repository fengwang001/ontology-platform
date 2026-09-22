package message_test

import (
	"errors"
	"testing"

	"ontology/message"
	"ontology/wire"
)

func offsetOf(t *testing.T, err error) int {
	t.Helper()
	var pe *wire.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("not a ParseError: %v", err)
	}
	return pe.Offset
}

func TestErrorVarintTooLong(t *testing.T) {
	// field number varint that never terminates, starts at 0
	in := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, wire.ErrVarintTooLong) || offsetOf(t, err) != 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestErrorUnknownWireType(t *testing.T) {
	in := []byte{0x01, 0x07}
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, wire.ErrUnknownWireType) || offsetOf(t, err) != 1 {
		t.Fatalf("err=%v", err)
	}
}

func TestErrorLengthOverflow(t *testing.T) {
	// field 2 (bytes, known), multi-byte length varint 300 starting
	// at offset 4, only one payload byte present.
	in := concat(encVarintField(1, 1), []byte{0x02, 0x01, 0xac, 0x02, 'a'})
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, wire.ErrLengthOverflow) || offsetOf(t, err) != 5 {
		t.Fatalf("err=%v", err)
	}
}

func TestErrorFieldNumberZero(t *testing.T) {
	in := []byte{0x00, 0x00}
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, wire.ErrFieldNumberZero) || offsetOf(t, err) != 1 {
		t.Fatalf("err=%v", err)
	}
}

func TestErrorNestedLengthMismatch(t *testing.T) {
	// field 4 message claims 3 bytes; its inner bytes-field claims a
	// 10-byte payload that spills past the declared nested region
	// (but inside the outer buffer, so it parses locally).
	in := []byte{0x04, 0x02, 0x03, 0x63, 0x01, 0x0a}
	in = append(in, []byte("abcdefghij")...)
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, wire.ErrNestedLength) || offsetOf(t, err) != 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestErrorsAreDistinguishable(t *testing.T) {
	sentinels := []error{
		wire.ErrVarintTooLong,
		wire.ErrUnknownWireType,
		wire.ErrLengthOverflow,
		wire.ErrFieldNumberZero,
		wire.ErrNestedLength,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d overlap", i, j)
			}
		}
	}
}

func TestErrorReturnsZeroMessage(t *testing.T) {
	in := concat(
		encVarintField(1, 42),
		encVarintField(50, 7),
		[]byte{0x02, 0x01, 0x32}, // bad bytes field near the end
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err == nil {
		t.Fatal("expected error")
	}
	if m != nil {
		t.Fatalf("partial message leaked: %#v", m)
	}
}

func TestTypeMismatchRejected(t *testing.T) {
	// field 1 declared varint but encoded as bytes
	in := encBytesField(1, []byte("x"))
	_, err := message.Parse(in, personSchema, defaultLimits)
	if !errors.Is(err, message.ErrTypeMismatch) {
		t.Fatalf("err=%v", err)
	}
}
