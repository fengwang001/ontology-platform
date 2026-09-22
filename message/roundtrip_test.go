package message_test

import (
	"bytes"
	"testing"

	"ontology/message"
	"ontology/wire"
)

func vid(num, v uint64) []byte { return wire.AppendVarintField(nil, num, v) }
func vbytes(num uint64, b []byte) []byte {
	return wire.AppendField(nil, num, wire.Bytes, b)
}
func vmsg(num uint64, b []byte) []byte {
	return wire.AppendField(nil, num, wire.Message, b)
}
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func mustParse(t *testing.T, data []byte) *message.Message {
	t.Helper()
	m, err := message.Parse(data, message.DefaultLimits())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestRoundTripByteIdentical(t *testing.T) {
	input := cat(
		vid(1, 150),
		vid(7, 1),               // unknown varint
		vbytes(2, []byte("hi")), // known name
		vbytes(9, []byte("zz")), // unknown bytes
		vid(7, 2),               // repeated unknown, same number
	)
	m := mustParse(t, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("round trip mismatch:\n got %x\nwant %x", got, input)
	}
	if m.ID() != 150 || string(m.Name()) != "hi" {
		t.Fatalf("known fields wrong: id=%d name=%q", m.ID(), m.Name())
	}
	if n := len(m.UnknownFields()); n != 3 {
		t.Fatalf("unknown count = %d, want 3", n)
	}
}

func TestInterleavedOrderPreserved(t *testing.T) {
	input := cat(
		vbytes(9, []byte("A")),
		vid(1, 1),
		vbytes(9, []byte("B")),
		vbytes(2, []byte("n")),
		vbytes(9, []byte("C")),
		vid(1, 2), // repeated known field
	)
	m := mustParse(t, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("interleaved round trip mismatch:\n got %x\nwant %x", got, input)
	}
	if m.ID() != 2 {
		t.Fatalf("ID = %d, want last occurrence 2", m.ID())
	}
}

func TestRepeatedUnknownsKeepOrder(t *testing.T) {
	input := cat(
		vid(5, 1), vid(5, 2), vid(5, 3),
		vbytes(5, []byte("x")), vid(5, 4),
	)
	m := mustParse(t, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("repeated unknowns reordered or merged:\n got %x\nwant %x", got, input)
	}
	if n := len(m.UnknownFields()); n != 5 {
		t.Fatalf("unknown count = %d, want 5 (no dedup)", n)
	}
}

func TestNestedUnknownsPreserved(t *testing.T) {
	inner := cat(vid(5, 1), vid(1, 7), vbytes(8, []byte("deep")))
	mid := cat(vbytes(2, []byte("m")), vmsg(3, inner), vid(6, 9))
	top := cat(vid(1, 1), vmsg(3, mid), vbytes(10, []byte("top")))

	m := mustParse(t, top)
	if got := m.Marshal(); !bytes.Equal(got, top) {
		t.Fatalf("nested round trip mismatch:\n got %x\nwant %x", got, top)
	}
	child := m.Child()
	if child == nil || child.Child() == nil {
		t.Fatal("nested children missing")
	}
	if n := len(child.Child().UnknownFields()); n != 2 {
		t.Fatalf("inner unknown count = %d, want 2", n)
	}
	if n := len(child.UnknownFields()); n != 1 {
		t.Fatalf("mid unknown count = %d, want 1", n)
	}
}

func TestNonCanonicalVarintRoundTrip(t *testing.T) {
	// 0x81 0x00 is a legal but non-canonical encoding of 1.
	input := cat(
		[]byte{0x07, byte(wire.Varint), 0x81, 0x00}, // unknown field 7 = 1
		vid(1, 5),
	)
	m := mustParse(t, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("non-canonical varint not preserved:\n got %x\nwant %x", got, input)
	}
}
