package message_test

import (
	"bytes"
	"testing"
)

func TestMarshalIdempotent(t *testing.T) {
	input := cat(
		vid(1, 1),
		vbytes(9, []byte("A")),
		vmsg(3, cat(vid(5, 1), vid(1, 7))),
		vbytes(2, []byte("n")),
	)
	m := mustParse(t, input)
	first := m.Marshal()
	second := m.Marshal()
	if !bytes.Equal(first, second) {
		t.Fatalf("marshal not idempotent:\n1st %x\n2nd %x", first, second)
	}
	if !bytes.Equal(first, input) {
		t.Fatalf("marshal mismatch:\n got %x\nwant %x", first, input)
	}
	// Idempotency must also hold after edits.
	m.SetID(9)
	a := m.Marshal()
	b := m.Marshal()
	if !bytes.Equal(a, b) {
		t.Fatalf("marshal after edit not idempotent:\n1st %x\n2nd %x", a, b)
	}
}

func TestParsedMessageIndependentOfInput(t *testing.T) {
	input := cat(
		vid(1, 42),
		vbytes(2, []byte("name")),
		vbytes(9, []byte("unknown")),
	)
	snapshot := dupBytes(input)
	m := mustParse(t, input)

	// Corrupt the input buffer after parsing.
	for i := range input {
		input[i] ^= 0xFF
	}

	if got := m.Marshal(); !bytes.Equal(got, snapshot) {
		t.Fatalf("parsed message affected by input mutation:\n got %x\nwant %x", got, snapshot)
	}
	if m.ID() != 42 || string(m.Name()) != "name" {
		t.Fatalf("known fields corrupted: id=%d name=%q", m.ID(), m.Name())
	}
	unk := m.UnknownFields()
	if len(unk) != 1 || !bytes.Equal(unk[0].Raw, snapshot[len(snapshot)-10:]) {
		t.Fatalf("unknown field corrupted: %+v", unk)
	}
}

func TestSetNameCopiesInput(t *testing.T) {
	m := mustParse(t, vid(1, 1))
	name := []byte("abc")
	m.SetName(name)
	name[0] = 'X'
	if string(m.Name()) != "abc" {
		t.Fatalf("SetName aliased caller buffer: %q", m.Name())
	}
}

func TestNameGetterReturnsCopy(t *testing.T) {
	m := mustParse(t, vbytes(2, []byte("abc")))
	n := m.Name()
	n[0] = 'X'
	if string(m.Name()) != "abc" {
		t.Fatalf("Name exposed internal buffer: %q", m.Name())
	}
}

func dupBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
