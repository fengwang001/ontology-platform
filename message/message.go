package message

import "ontology/unknown"

// Known field numbers of Envelope.
const (
	fieldID    = 1 // varint
	fieldName  = 2 // bytes
	fieldTags  = 3 // bytes, repeated
	fieldChild = 4 // message
)

// occurrence is the preserved raw encoding of one known-field
// occurrence, used to reproduce the input byte-exactly when the
// value was not modified.
type occurrence struct {
	raw []byte // complete field encoding
}

// childOccurrence additionally keeps the nested payload for
// change detection against a re-serialized child.
type childOccurrence struct {
	raw     []byte
	payload []byte
}

// ref records one entry of the original field sequence. Byte-exact
// round-tripping with arbitrarily interleaved known and unknown
// fields is only possible if the original order is remembered, so
// the parser appends one ref per field occurrence.
type ref struct {
	field uint64
	known bool
}

// Envelope is the known message structure. Fields unknown to this
// version are kept in Unknown and re-emitted at their original
// positions. A parsed Envelope is owned by its caller; serializing
// it never mutates it.
type Envelope struct {
	ID      uint64   // field 1; present only if HasID
	Name    []byte   // field 2; present only if HasName
	Tags    [][]byte // field 3, repeated
	Child   *Envelope
	Unknown unknown.Fields

	hasID    bool
	hasName  bool
	origID   uint64
	origName []byte
	origTags [][]byte
	idOcc    []occurrence
	nameOcc  []occurrence
	tagOcc   []occurrence
	childOcc []childOccurrence
	order    []ref
}

// HasID reports whether field 1 is present.
func (e *Envelope) HasID() bool { return e.hasID }

// HasName reports whether field 2 is present.
func (e *Envelope) HasName() bool { return e.hasName }

// SetID sets field 1 and marks it present.
func (e *Envelope) SetID(v uint64) { e.ID, e.hasID = v, true }

// ClearID removes field 1 from the serialized output.
func (e *Envelope) ClearID() { e.hasID = false }

// SetName sets field 2 and marks it present. The slice is copied.
func (e *Envelope) SetName(b []byte) {
	e.Name = append([]byte(nil), b...)
	e.hasName = true
}

// ClearName removes field 2 from the serialized output.
func (e *Envelope) ClearName() { e.hasName = false }
