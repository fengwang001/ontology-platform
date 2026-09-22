package message_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/message"
	"ontology/wire"
)

// badInputs builds one input per syntax error kind, with the expected
// absolute offset of the offending bytes.
func badInputs() map[wire.Kind]struct {
	data   []byte
	offset int
} {
	varintOverflow := append(bytes.Repeat([]byte{0x80}, 10), 0x01)
	unknownWireType := []byte{0x01, 0x07}
	lengthOverflow := []byte{0x02, byte(wire.Bytes), 0x05, 0xAA}
	fieldNumberZero := []byte{0x00, byte(wire.Varint), 0x01}
	// Nested message declares length 2, but its inner field 2 claims a
	// 5-byte payload that fits in the outer buffer: the inner field
	// crosses the declared nested boundary.
	nestedMismatch := cat(
		[]byte{0x03, byte(wire.Message), 0x02},
		[]byte{0x02, byte(wire.Bytes), 0x05},
		[]byte("abcde"),
	)
	return map[wire.Kind]struct {
		data   []byte
		offset int
	}{
		wire.KindVarintOverflow:       {varintOverflow, 0},
		wire.KindUnknownWireType:      {unknownWireType, 1},
		wire.KindLengthOverflow:       {lengthOverflow, 2},
		wire.KindFieldNumberZero:      {fieldNumberZero, 0},
		wire.KindNestedLengthMismatch: {nestedMismatch, 3},
	}
}

func TestSyntaxErrorsDistinguishable(t *testing.T) {
	seen := map[wire.Kind]bool{}
	for wantKind, tc := range badInputs() {
		m, err := message.Parse(tc.data, message.DefaultLimits())
		if err == nil {
			t.Fatalf("kind %d: expected error, got nil", wantKind)
		}
		var we *wire.Error
		if !errors.As(err, &we) {
			t.Fatalf("kind %d: want *wire.Error, got %T: %v", wantKind, err, err)
		}
		if we.Kind != wantKind {
			t.Fatalf("want kind %d, got kind %d (%v)", wantKind, we.Kind, err)
		}
		if we.Offset != tc.offset {
			t.Fatalf("kind %d: offset = %d, want %d", wantKind, we.Offset, tc.offset)
		}
		if seen[we.Kind] {
			t.Fatalf("kind %d reported twice", we.Kind)
		}
		seen[we.Kind] = true
		if m != nil {
			t.Fatalf("kind %d: error returned non-nil message", wantKind)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("only %d distinct kinds seen", len(seen))
	}
}

func TestErrorSentinelsMatch(t *testing.T) {
	sents := map[wire.Kind]error{
		wire.KindVarintOverflow:       wire.ErrVarintOverflow,
		wire.KindUnknownWireType:      wire.ErrUnknownWireType,
		wire.KindLengthOverflow:       wire.ErrLengthOverflow,
		wire.KindFieldNumberZero:      wire.ErrFieldNumberZero,
		wire.KindNestedLengthMismatch: wire.ErrNestedLengthMismatch,
	}
	for kind, tc := range badInputs() {
		_, err := message.Parse(tc.data, message.DefaultLimits())
		if !errors.Is(err, sents[kind]) {
			t.Fatalf("kind %d: errors.Is(%v) failed", kind, sents[kind])
		}
		for otherKind, other := range sents {
			if otherKind != kind && errors.Is(err, other) {
				t.Fatalf("kind %d also matches sentinel of kind %d", kind, otherKind)
			}
		}
	}
}

func TestNestedErrorOffsetIsAbsolute(t *testing.T) {
	// Unknown field (3 bytes), then a nested message whose inner field
	// number is 0 at absolute offset 3+3=6.
	inner := []byte{0x00, byte(wire.Varint), 0x01}
	input := cat(vid(9, 1), vmsg(3, inner))
	_, err := message.Parse(input, message.DefaultLimits())
	var we *wire.Error
	if !errors.As(err, &we) {
		t.Fatalf("want *wire.Error, got %v", err)
	}
	if we.Kind != wire.KindFieldNumberZero || we.Offset != 6 {
		t.Fatalf("got kind %d offset %d, want kind %d offset 6",
			we.Kind, we.Offset, wire.KindFieldNumberZero)
	}
}
