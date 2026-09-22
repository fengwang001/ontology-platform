// Package message parses and serializes the known message structure
// while preserving unrecognized fields verbatim, so that an old parser
// can forward a message without losing fields added by newer versions.
//
// It depends on the wire and unknown packages (and nothing else in
// this module).
//
// Known schema:
//
//	field 1: id    varint
//	field 2: name  bytes (UTF-8 string)
//	field 3: child message (nested Message, recursive)
//
// Ordering rule, derived from the byte-equivalence invariant: because
// known and unknown fields may be interleaved arbitrarily and the
// re-encoded output must equal the input byte for byte, fields are
// re-emitted in their exact input order. A Message therefore keeps one
// ordered slot per input field occurrence; unknown occurrences live in
// an unknown.Set and are referenced from the slot sequence.
package message

import (
	"errors"

	"ontology/unknown"
)

// Limit-exceeded errors. They are distinct from the wire syntax error
// categories and from each other; use errors.Is to test them.
var (
	// ErrMessageTooLarge: input exceeds Options.MaxMessageBytes.
	ErrMessageTooLarge = errors.New("message: input exceeds MaxMessageBytes")
	// ErrPayloadTooLarge: a field payload exceeds Options.MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("message: field payload exceeds MaxPayloadBytes")
	// ErrTooManyUnknownFields: unknown fields exceed Options.MaxUnknownFields.
	ErrTooManyUnknownFields = errors.New("message: unknown fields exceed MaxUnknownFields")
	// ErrDepthExceeded: nesting exceeds Options.MaxDepth.
	ErrDepthExceeded = errors.New("message: nesting exceeds MaxDepth")
)

// Options configures parser limits. A zero field falls back to the
// DefaultOptions value, so Options{} is equivalent to DefaultOptions.
type Options struct {
	MaxMessageBytes  int // maximum size of one encoded message
	MaxPayloadBytes  int // maximum size of one field payload
	MaxUnknownFields int // maximum number of unknown fields per message
	MaxDepth         int // maximum nesting level of message fields
}

// DefaultOptions returns the limits used when Parse gets a nil *Options.
func DefaultOptions() Options {
	return Options{
		MaxMessageBytes:  1 << 20,
		MaxPayloadBytes:  1 << 20,
		MaxUnknownFields: 4096,
		MaxDepth:         64,
	}
}

// withDefaults replaces non-positive fields with the defaults.
func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = d.MaxMessageBytes
	}
	if o.MaxPayloadBytes <= 0 {
		o.MaxPayloadBytes = d.MaxPayloadBytes
	}
	if o.MaxUnknownFields <= 0 {
		o.MaxUnknownFields = d.MaxUnknownFields
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = d.MaxDepth
	}
	return o
}

// Known field numbers.
const (
	fieldID    = 1 // varint
	fieldName  = 2 // bytes
	fieldChild = 3 // message
)

// slotKind classifies one occurrence in the ordered field sequence.
type slotKind uint8

const (
	slotUnknown slotKind = iota // bytes live in the unknown.Set
	slotVarint                  // known varint field
	slotBytes                   // known bytes field
	slotMessage                 // known nested message field
)

// slot is one field occurrence, in input order.
type slot struct {
	num    uint32
	kind   slotKind
	vint   uint64   // slotVarint value
	data   []byte   // slotBytes value (owned copy)
	child  *Message // slotMessage value
	unkIdx int      // slotUnknown: index into Message.unk
}

// Message is a parsed message. It is owned by its caller: Parse never
// aliases the input buffer, and Marshal never mutates the Message.
// The zero value is an empty message.
type Message struct {
	slots []slot
	unk   unknown.Set
}

// UnknownFields returns the preserved unknown fields in input order.
// The returned Field.Data slices are read-only.
func (m *Message) UnknownFields() []unknown.Field {
	out := make([]unknown.Field, 0, m.unk.Len())
	for _, s := range m.slots {
		if s.kind == slotUnknown {
			out = append(out, m.unk.Field(s.unkIdx))
		}
	}
	return out
}
