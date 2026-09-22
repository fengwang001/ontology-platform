package message

import (
	"bytes"
	"testing"
)

func TestModifyKnownKeepsUnknownPosition(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(
		vfield(7, 9),           // unknown, before
		vfield(1, 5),           // known, will change
		bfield(8, []byte("z")), // unknown, after
	)
	m := mustParse(t, p, input)
	m.SetUint(1, 300)
	want := cat(vfield(7, 9), vfield(1, 300), bfield(8, []byte("z")))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestModifyRepeatedKnownCollapsesToFirstPosition(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(1, 1), vfield(7, 9), vfield(1, 2))
	m := mustParse(t, p, input)
	m.SetUint(1, 42)
	// The new value takes the first occurrence's slot; the unknown field
	// keeps its place relative to the remaining known fields.
	want := cat(vfield(1, 42), vfield(7, 9))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDeleteKnownKeepsUnknownOrder(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(
		vfield(7, 1),           // unknown a
		vfield(1, 5),           // known, deleted
		bfield(8, []byte("z")), // unknown b
		vfield(7, 2),           // unknown c, duplicate number
		bfield(2, []byte("k")), // known, stays
	)
	m := mustParse(t, p, input)
	m.Delete(1)
	want := cat(vfield(7, 1), bfield(8, []byte("z")), vfield(7, 2), bfield(2, []byte("k")))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSetNewFieldAppendsAtEnd(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(7, 1), vfield(1, 5))
	m := mustParse(t, p, input)
	m.SetBytes(2, []byte("new"))
	want := cat(vfield(7, 1), vfield(1, 5), bfield(2, []byte("new")))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDeleteThenSetRoundTrip(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(7, 1), vfield(1, 5))
	m := mustParse(t, p, input)
	m.Delete(1)
	m.SetUint(1, 6)
	want := cat(vfield(7, 1), vfield(1, 6))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNestedModificationPropagates(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(9, 1), mfield(3, cat(vfield(1, 1), vfield(8, 2))))
	m := mustParse(t, p, input)
	sub, ok := m.Nested(3)
	if !ok {
		t.Fatal("missing nested message")
	}
	sub.SetUint(1, 77)
	want := cat(vfield(9, 1), mfield(3, cat(vfield(1, 77), vfield(8, 2))))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSettersIgnoreWrongKind(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(1, 5), vfield(7, 1))
	m := mustParse(t, p, input)
	m.SetBytes(1, []byte("x")) // field 1 is varint
	m.SetUint(2, 3)            // field 2 is bytes
	m.SetUint(99, 1)           // unknown number
	if got := m.Marshal(); !bytes.Equal(got, input) {
		t.Fatalf("wrong-kind setter changed message: %v", got)
	}
}
