// Package message parses and serializes known message structures on top
// of the wire and unknown packages, preserving unrecognized fields so
// that a parse followed by a write-back reproduces the input byte for
// byte.
//
// Ordering rule (derived from the byte-equivalence invariant): a message
// is a single ordered sequence of fields in which known and unknown
// fields may interleave arbitrarily. Because write-back must reproduce
// the input exactly, fields are written back in their original wire
// order; unknown fields therefore stay exactly where they appeared
// relative to known ones. When a known field is modified, its new
// encoding takes the position of its first occurrence; when it is
// deleted, its occurrences are removed and the remaining fields keep
// their relative order. Newly added known fields are appended at the end,
// ordered by field number.
package message

import (
	"errors"

	"ontology/wire"
)

// Limit errors, wrapped in *wire.Error with the relevant offset.
var (
	// ErrMessageTooLarge: input exceeds Options.MaxMessageBytes.
	ErrMessageTooLarge = errors.New("message: input exceeds maximum message size")
	// ErrFieldTooLarge: a field payload exceeds Options.MaxFieldBytes.
	ErrFieldTooLarge = errors.New("message: field payload exceeds maximum field size")
	// ErrTooManyUnknownFields: unknown field count exceeds
	// Options.MaxUnknownFields.
	ErrTooManyUnknownFields = errors.New("message: too many unknown fields")
	// ErrMaxDepth: nesting exceeds Options.MaxDepth.
	ErrMaxDepth = errors.New("message: maximum nesting depth exceeded")
)

// Schema declares the known fields of a message: field number to expected
// wire type. Nested messages (wire.Message) use the same schema
// recursively. A field number absent from the schema, or present with a
// different wire type than encountered, is treated as unknown.
type Schema map[int]wire.Type

// Options configures a Parser. Zero-valued fields fall back to the
// Default values.
type Options struct {
	// MaxMessageBytes bounds the size of one top-level message.
	MaxMessageBytes int
	// MaxFieldBytes bounds the payload of a single bytes/message field.
	MaxFieldBytes int
	// MaxUnknownFields bounds the total number of unknown fields in one
	// message, counting nested messages recursively.
	MaxUnknownFields int
	// MaxDepth bounds the nesting depth of message-typed fields.
	MaxDepth int
}

// Default values applied to zero-valued Options fields.
const (
	DefaultMaxMessageBytes  = 1 << 20
	DefaultMaxFieldBytes    = 1 << 20
	DefaultMaxUnknownFields = 1 << 14
	DefaultMaxDepth         = 64
)

func (o Options) withDefaults() Options {
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if o.MaxFieldBytes <= 0 {
		o.MaxFieldBytes = DefaultMaxFieldBytes
	}
	if o.MaxUnknownFields <= 0 {
		o.MaxUnknownFields = DefaultMaxUnknownFields
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	return o
}

// Parser parses and re-encodes messages of one schema. It is immutable
// after construction and safe for concurrent use.
type Parser struct {
	schema Schema
	opts   Options
}

// NewParser returns a Parser for schema with the given options.
func NewParser(schema Schema, opts Options) *Parser {
	sc := make(Schema, len(schema))
	for num, t := range schema {
		sc[num] = t
	}
	return &Parser{schema: sc, opts: opts.withDefaults()}
}
