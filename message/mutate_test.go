package message_test

import (
	"bytes"
	"testing"

	"ontology/message"
)

func TestModifyKnownKeepsUnknownPositions(t *testing.T) {
	in := concat(
		encVarintField(50, 11), // unknown
		encVarintField(1, 1),   // known, will change value/size
		encVarintField(51, 22), // unknown
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	m.SetVarint(1, 300000000)
	out, _ := m.Marshal()
	want := concat(
		encVarintField(50, 11),
		encVarintField(1, 300000000),
		encVarintField(51, 22),
	)
	if !bytes.Equal(out, want) {
		t.Fatalf("modify reorders/loses unknowns:\n got=%v\nwant=%v", out, want)
	}
}

func TestDeleteKnownKeepsRelativeOrder(t *testing.T) {
	in := concat(
		encVarintField(50, 11),
		encVarintField(1, 1),
		encVarintField(3, 30),
		encVarintField(51, 22),
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Delete(1) {
		t.Fatal("delete reported false")
	}
	out, _ := m.Marshal()
	want := concat(
		encVarintField(50, 11),
		encVarintField(3, 30),
		encVarintField(51, 22),
	)
	if !bytes.Equal(out, want) {
		t.Fatalf("delete reorders fields:\n got=%v\nwant=%v", out, want)
	}
}

func TestRepeatedMarshalIdempotent(t *testing.T) {
	in := concat(encVarintField(1, 1), encBytesField(2, []byte("ab")))
	m, _ := message.Parse(in, personSchema, defaultLimits)
	a, _ := m.Marshal()
	m.SetVarint(1, 2)
	b, _ := m.Marshal()
	c, _ := m.Marshal()
	if !bytes.Equal(b, c) {
		t.Fatal("marshalling twice after modify differs")
	}
	if bytes.Equal(a, b) {
		t.Fatal("expected change")
	}
}

func TestInputMutationDoesNotAffectParsed(t *testing.T) {
	in := concat(encBytesField(2, []byte("hello")), encBytesField(99, []byte("world")))
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		in[i] = 0xff
	}
	got, ok := m.Bytes(2)
	if !ok || string(got) != "hello" {
		t.Fatalf("known payload aliases input: %q", got)
	}
	out, _ := m.Marshal()
	if bytes.Contains(out, []byte{0xff, 0xff}) {
		t.Fatalf("unknown payload aliases input: %v", out)
	}
	u := m.Unknowns()
	if string(u.At(0).Payload) != "world" {
		t.Fatalf("unknown payload aliases input: %q", u.At(0).Payload)
	}
}
