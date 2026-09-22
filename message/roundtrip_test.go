package message

import (
	"bytes"
	"testing"
)

func TestRoundTripByteExact(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(
		vfield(1, 150),            // known varint
		vfield(7, 9),              // unknown varint
		bfield(2, []byte("hi")),   // known bytes
		bfield(9, []byte("xy")),   // unknown bytes
		mfield(3, vfield(8, 1)),   // known message with unknown inside
		mfield(12, vfield(5, 42)), // unknown message
	)
	m := mustParse(t, p, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("round trip changed bytes:\n in=%v\nout=%v", input, got)
	}
}

func TestRoundTripNonCanonicalVarint(t *testing.T) {
	p := newParser(t, Options{})
	// Known varint field 1 with a zero-padded (non-canonical) encoding.
	input := []byte{0x01, 0x00, 0x81, 0x00}
	m := mustParse(t, p, input)
	if v, ok := m.Uint(1); !ok || v != 1 {
		t.Fatalf("Uint(1) = %d, %v", v, ok)
	}
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("non-canonical encoding not preserved: %v", got)
	}
}

func TestInterleaveAndDuplicateOrder(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(
		vfield(1, 1),
		vfield(7, 1), // unknown
		vfield(1, 2), // repeated known
		vfield(7, 2), // repeated unknown
		bfield(2, []byte("a")),
		vfield(7, 3),
	)
	m := mustParse(t, p, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("order not preserved:\n in=%v\nout=%v", input, got)
	}
	if got := len(m.Unknowns()); got != 3 {
		t.Fatalf("unknown count = %d, want 3 (no dedup/merge)", got)
	}
	// Last occurrence wins for the singular known accessor.
	if v, _ := m.Uint(1); v != 2 {
		t.Fatalf("Uint(1) = %d, want 2", v)
	}
}

func TestNestedUnknownRecursive(t *testing.T) {
	p := newParser(t, Options{})
	inner := cat(vfield(1, 1), vfield(50, 1))   // depth 3, unknown 50
	mid := cat(vfield(40, 2), mfield(3, inner)) // depth 2, unknown 40
	top := cat(vfield(30, 3), mfield(3, mid))   // depth 1, unknown 30
	input := cat(vfield(1, 7), mfield(3, top))
	m := mustParse(t, p, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("nested round trip changed bytes:\n in=%v\nout=%v", input, got)
	}
	l1, _ := m.Nested(3)
	l2, _ := l1.Nested(3)
	l3, _ := l2.Nested(3)
	if len(l1.Unknowns()) != 1 || len(l2.Unknowns()) != 1 || len(l3.Unknowns()) != 1 {
		t.Fatal("unknown fields lost at some nesting level")
	}
}

func TestKnownNumberWrongTypeIsUnknown(t *testing.T) {
	p := newParser(t, Options{})
	// Field 1 is known as varint; a bytes-typed field 1 is unknown.
	input := cat(bfield(1, []byte("x")), vfield(1, 5))
	m := mustParse(t, p, input)
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("out=%v", got)
	}
	if len(m.Unknowns()) != 1 {
		t.Fatal("wrong-typed known number should be unknown")
	}
}

func TestMarshalIdempotent(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(1, 1), vfield(7, 2), mfield(3, vfield(9, 3)))
	m := mustParse(t, p, input)
	first := m.Marshal()
	second := m.Marshal()
	if !bytes.Equal(first, second) || !bytes.Equal(first, input) {
		t.Fatalf("marshal not idempotent: %v vs %v", first, second)
	}
	m.SetUint(1, 99)
	if !bytes.Equal(m.Marshal(), m.Marshal()) {
		t.Fatal("marshal after mutation not idempotent")
	}
}

func TestParseDoesNotAliasInput(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(1, 5), bfield(2, []byte("abc")), vfield(7, 8))
	m := mustParse(t, p, input)
	snapshot := m.Marshal()
	for i := range input {
		input[i] = 0xff
	}
	if got := m.Marshal(); !bytes.Equal(got, snapshot) {
		t.Fatalf("input mutation leaked into parsed message: %v", got)
	}
	if v, _ := m.Uint(1); v != 5 {
		t.Fatalf("Uint(1) = %d after input corruption", v)
	}
	if b, _ := m.Bytes(2); string(b) != "abc" {
		t.Fatalf("Bytes(2) = %q after input corruption", b)
	}
}
