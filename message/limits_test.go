package message

import (
	"errors"
	"ontology/wire"
	"testing"
)

func TestMaxMessageBytes(t *testing.T) {
	p := newParser(t, Options{MaxMessageBytes: 4})
	input := cat(vfield(1, 1), vfield(7, 2)) // 6 bytes
	m, err := p.Parse(input)
	if !errors.Is(err, ErrMessageTooLarge) || m != nil {
		t.Fatalf("got %v (msg %v), want ErrMessageTooLarge and nil", err, m)
	}
	var we *wire.Error
	if errors.As(err, &we) && we.Offset != 0 {
		t.Fatalf("offset = %d, want 0", we.Offset)
	}
	if _, err := p.Parse(input[:3]); err != nil {
		t.Fatalf("input at limit rejected: %v", err)
	}
}

func TestMaxFieldBytes(t *testing.T) {
	p := newParser(t, Options{MaxFieldBytes: 3})
	input := cat(vfield(1, 1), bfield(2, []byte("toolong")))
	m, err := p.Parse(input)
	if !errors.Is(err, ErrFieldTooLarge) || m != nil {
		t.Fatalf("got %v (msg %v), want ErrFieldTooLarge and nil", err, m)
	}
	// The limit applies to unknown fields too, and is checked before the
	// payload is retained.
	input = bfield(9, []byte("toolong"))
	if _, err := p.Parse(input); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("unknown field: got %v", err)
	}
	if _, err := p.Parse(bfield(2, []byte("fit"))); err != nil {
		t.Fatalf("payload at limit rejected: %v", err)
	}
}

func TestMaxUnknownFields(t *testing.T) {
	p := newParser(t, Options{MaxUnknownFields: 2})
	input := cat(vfield(7, 1), vfield(8, 2), vfield(9, 3))
	m, err := p.Parse(input)
	if !errors.Is(err, ErrTooManyUnknownFields) || m != nil {
		t.Fatalf("got %v (msg %v), want ErrTooManyUnknownFields and nil", err, m)
	}
	// Unknown fields inside nested messages count too.
	nested := cat(vfield(7, 1), mfield(3, cat(vfield(8, 2), vfield(9, 3))))
	if _, err := p.Parse(nested); !errors.Is(err, ErrTooManyUnknownFields) {
		t.Fatalf("nested: got %v", err)
	}
	if _, err := p.Parse(cat(vfield(7, 1), vfield(8, 2))); err != nil {
		t.Fatalf("count at limit rejected: %v", err)
	}
}

func TestMaxDepth(t *testing.T) {
	p := newParser(t, Options{MaxDepth: 2})
	// depth 1 -> 2 -> 3: the innermost exceeds the limit.
	input := mfield(3, mfield(3, mfield(3, vfield(1, 1))))
	m, err := p.Parse(input)
	if !errors.Is(err, ErrMaxDepth) || m != nil {
		t.Fatalf("got %v (msg %v), want ErrMaxDepth and nil", err, m)
	}
	if _, err := p.Parse(mfield(3, mfield(3, vfield(1, 1)))); err != nil {
		t.Fatalf("depth at limit rejected: %v", err)
	}
	// Unknown message-typed fields are bounded too.
	deep := mfield(9, mfield(9, mfield(9, mfield(9, nil))))
	if _, err := p.Parse(deep); !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("unknown nesting: got %v", err)
	}
}

func TestLimitErrorsDistinguishable(t *testing.T) {
	all := []error{ErrMessageTooLarge, ErrFieldTooLarge, ErrTooManyUnknownFields, ErrMaxDepth}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d not distinguishable", i, j)
			}
		}
	}
}
