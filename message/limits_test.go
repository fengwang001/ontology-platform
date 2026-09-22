package message_test

import (
	"errors"
	"testing"

	"ontology/message"
	"ontology/unknown"
	"ontology/wire"
)

func TestLimitMessageSize(t *testing.T) {
	in := encBytesField(2, make([]byte, 50))
	_, err := message.Parse(in, personSchema, message.Limits{MaxMessageBytes: 10})
	if !errors.Is(err, wire.ErrSizeLimit) {
		t.Fatalf("got %v", err)
	}
}

func TestLimitFieldPayload(t *testing.T) {
	in := encBytesField(2, make([]byte, 50))
	_, err := message.Parse(in, personSchema, message.Limits{MaxFieldPayloadBytes: 10})
	if !errors.Is(err, wire.ErrFieldSizeLimit) {
		t.Fatalf("got %v", err)
	}
}

func TestLimitUnknownCount(t *testing.T) {
	in := concat(
		encVarintField(90, 1),
		encVarintField(91, 2),
		encVarintField(92, 3),
	)
	m, err := message.Parse(in, personSchema, message.Limits{MaxUnknown: 2})
	if !errors.Is(err, unknown.ErrTooManyUnknown) {
		t.Fatalf("got %v (msg=%v)", err, m)
	}
	if m != nil {
		t.Fatal("partial state leaked")
	}
}

func TestLimitNesting(t *testing.T) {
	// person(depth1).outer(depth2).inner(depth3) exceeds limit 2.
	full := encMessageField(5, encMessageField(1, encVarintField(1, 1)))
	_, err := message.Parse(full, personSchema, message.Limits{MaxNesting: 2})
	if !errors.Is(err, message.ErrTooDeep) {
		t.Fatalf("got %v", err)
	}
}

func TestLimitNestingAllowed(t *testing.T) {
	full := encMessageField(5, encMessageField(1, encVarintField(1, 1)))
	m, err := message.Parse(full, personSchema, message.Limits{MaxNesting: 3})
	if err != nil {
		t.Fatalf("depth 3 with limit 3 should pass: %v", err)
	}
	out, _ := m.Marshal()
	if len(out) == 0 {
		t.Fatal("empty output")
	}
}
