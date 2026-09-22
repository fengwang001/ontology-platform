package message_test

import (
	"errors"
	"testing"

	"ontology/message"
)

func limitsWith(fn func(*message.Limits)) message.Limits {
	l := message.DefaultLimits()
	fn(&l)
	return l
}

func TestMaxMessageBytes(t *testing.T) {
	input := cat(vid(1, 1), vbytes(2, []byte("0123456789")))
	lim := limitsWith(func(l *message.Limits) { l.MaxMessageBytes = len(input) - 1 })
	m, err := message.Parse(input, lim)
	if !errors.Is(err, message.ErrMessageTooLarge) {
		t.Fatalf("want ErrMessageTooLarge, got %v", err)
	}
	if m != nil {
		t.Fatal("limit violation returned non-nil message")
	}
}

func TestMaxPayloadBytes(t *testing.T) {
	input := cat(vid(1, 1), vbytes(2, []byte("0123456789")))
	lim := limitsWith(func(l *message.Limits) { l.MaxPayloadBytes = 5 })
	m, err := message.Parse(input, lim)
	if !errors.Is(err, message.ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
	if m != nil {
		t.Fatal("limit violation returned non-nil message")
	}
}

func TestMaxPayloadBytesAppliesToNested(t *testing.T) {
	inner := cat(vid(1, 1), vid(2, 2))
	input := vmsg(3, inner)
	lim := limitsWith(func(l *message.Limits) { l.MaxPayloadBytes = len(inner) - 1 })
	if _, err := message.Parse(input, lim); !errors.Is(err, message.ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge for nested payload, got %v", err)
	}
}

func TestMaxUnknownFields(t *testing.T) {
	input := cat(vid(7, 1), vid(8, 2), vid(9, 3))
	lim := limitsWith(func(l *message.Limits) { l.MaxUnknownFields = 2 })
	m, err := message.Parse(input, lim)
	if !errors.Is(err, message.ErrTooManyUnknowns) {
		t.Fatalf("want ErrTooManyUnknowns, got %v", err)
	}
	if m != nil {
		t.Fatal("limit violation returned non-nil message")
	}
}

func TestMaxUnknownFieldsCountsNested(t *testing.T) {
	inner := cat(vid(7, 1), vid(8, 2))
	input := cat(vid(9, 0), vmsg(3, inner))
	lim := limitsWith(func(l *message.Limits) { l.MaxUnknownFields = 2 })
	if _, err := message.Parse(input, lim); !errors.Is(err, message.ErrTooManyUnknowns) {
		t.Fatalf("nested unknowns not counted: got %v", err)
	}
}

func TestMaxDepth(t *testing.T) {
	inner := cat(vid(1, 1))
	deep := vmsg(3, vmsg(3, vmsg(3, inner))) // 4 levels total
	lim := limitsWith(func(l *message.Limits) { l.MaxDepth = 3 })
	m, err := message.Parse(deep, lim)
	if !errors.Is(err, message.ErrDepthExceeded) {
		t.Fatalf("want ErrDepthExceeded, got %v", err)
	}
	if m != nil {
		t.Fatal("limit violation returned non-nil message")
	}
	// Same input parses fine with sufficient depth.
	if _, err := message.Parse(deep, message.DefaultLimits()); err != nil {
		t.Fatalf("default limits should accept 4 levels: %v", err)
	}
}

func TestLimitsRejectBeforeConsumingRest(t *testing.T) {
	// The payload limit is hit on the first field; the trailing garbage
	// must never be parsed, so the error is the limit error, not a
	// syntax error from the garbage.
	input := cat(vbytes(2, []byte("0123456789")), []byte{0xFF, 0xFF, 0xFF})
	lim := limitsWith(func(l *message.Limits) { l.MaxPayloadBytes = 5 })
	_, err := message.Parse(input, lim)
	if !errors.Is(err, message.ErrPayloadTooLarge) {
		t.Fatalf("want ErrPayloadTooLarge, got %v", err)
	}
}
