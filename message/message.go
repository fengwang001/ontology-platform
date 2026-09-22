// Package message parses and re-serializes the known message structure.
// It depends on the wire and unknown packages only.
//
// Known schema:
//
//	field 1 (varint):  ID
//	field 2 (bytes):   Name
//	field 3 (message): Child (nested Message)
//
// A field is "known" only when both its number and its wire type match
// the schema; any other field is preserved as unknown.
//
// Ordering rule, derived from the byte-identical round-trip invariant:
// known and unknown fields may be interleaved arbitrarily in the input,
// so on write every field must be re-emitted at its original position in
// the field sequence. Any regrouping (e.g. sorting by field number or
// moving unknown fields to the tail) would corrupt interleaved inputs.
// To guarantee byte identity, each parsed field occurrence keeps its
// original raw bytes; only fields explicitly modified through the API are
// re-encoded, in place, at their original slot.
package message

import "ontology/unknown"

// Field numbers of the known schema.
const (
	fieldID    = 1
	fieldName  = 2
	fieldChild = 3
)

// slotKind identifies which occurrence list a slot refers to.
type slotKind uint8

const (
	slotID slotKind = iota
	slotName
	slotChild
	slotUnknown
)

// slot is one position in the original field sequence.
type slot struct {
	kind slotKind
	idx  int // index into the corresponding occurrence list
}

// idOcc is one occurrence of field 1.
type idOcc struct {
	raw   []byte // original encoding; nil once modified
	val   uint64
	dirty bool
}

// nameOcc is one occurrence of field 2.
type nameOcc struct {
	raw   []byte
	val   []byte
	dirty bool
}

// childOcc is one occurrence of field 3.
type childOcc struct {
	raw   []byte
	child *Message
	dirty bool
}

// Message is a parsed message. It owns all of its data: no slice aliases
// the input buffer it was parsed from. A Message is not safe for
// concurrent mutation, but independent Messages may be parsed, edited and
// marshaled concurrently.
type Message struct {
	ids    []idOcc
	names  []nameOcc
	childs []childOcc
	unk    unknown.Set
	order  []slot // interleaved known/unknown sequence, in wire order
}
