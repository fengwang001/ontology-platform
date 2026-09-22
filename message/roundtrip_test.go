package message_test

import (
	"bytes"
	"testing"

	"ontology/message"
)

var defaultLimits = message.Limits{}

func TestRoundTripUnknownPreserved(t *testing.T) {
	// field 1 known; fields 100 (varint), 101 (bytes), 102 (message) unknown
	in := concat(
		encVarintField(1, 42),
		encVarintField(100, 7),
		encBytesField(101, []byte("hello")),
		encMessageField(102, encVarintField(999, 3)),
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("round trip differs:\n in=%v\nout=%v", in, out)
	}
}

func TestInterleavedAndRepeatedOrder(t *testing.T) {
	// known 1, unknown 50, known tag 3 x2, unknown 50 again, known 2
	in := concat(
		encVarintField(1, 1),
		encVarintField(50, 11),
		encVarintField(3, 30),
		encVarintField(3, 31),
		encVarintField(50, 22),
		encBytesField(2, []byte("n")),
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := m.Marshal()
	if !bytes.Equal(out, in) {
		t.Fatalf("order changed:\n in=%v\nout=%v", in, out)
	}
	tags := m.Varints(3)
	if len(tags) != 2 || tags[0] != 30 || tags[1] != 31 {
		t.Fatalf("tags=%v", tags)
	}
	u := m.Unknowns()
	if u.Len() != 2 || u.At(0).Number != 50 || u.At(1).Number != 50 {
		t.Fatalf("unknowns=%+v", u.All())
	}
}

func TestNestedUnknownRecursion(t *testing.T) {
	// person.address (known) contains unknown field 77, inside an
	// unknown message envelope nesting three levels of unknown data.
	deep := encVarintField(88, 8)
	mid := encMessageField(77, deep)
	addr := concat(encVarintField(1, 200000), encBytesField(90, []byte("z")), encBytesField(2, []byte("NYC")))
	in := concat(
		encVarintField(1, 9),
		encMessageField(4, addr),
		encMessageField(5, encMessageField(1, mid)),
	)
	m, err := message.Parse(in, personSchema, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := m.Marshal()
	if !bytes.Equal(out, in) {
		t.Fatalf("nested round trip differs:\n in=%v\nout=%v", in, out)
	}
	gotAddr, ok := m.Message(4)
	if !ok {
		t.Fatal("missing address")
	}
	if gotAddr.Unknowns().Len() != 1 {
		t.Fatalf("nested unknown lost: %d", gotAddr.Unknowns().Len())
	}
}

func TestNilSchemaRoundTrip(t *testing.T) {
	in := concat(encVarintField(1, 5), encBytesField(2, []byte("x")))
	m, err := message.Parse(in, nil, defaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := m.Marshal()
	if !bytes.Equal(out, in) {
		t.Fatalf("nil schema changed bytes")
	}
}
