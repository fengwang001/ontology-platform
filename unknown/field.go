// Package unknown retains fields the parser does not recognise so
// that they can be written back byte-for-byte. It deliberately
// depends on no other package in this module: its wire-type byte
// values contractually match those of the wire package
// (Varint=0, Bytes=1, Message=2).
package unknown

import "errors"

// Wire-type bytes. These must stay numerically identical to the
// constants in package wire.
const (
	Varint  byte = 0
	Bytes   byte = 1
	Message byte = 2
)

// ErrTooManyUnknown is returned when adding a field would exceed the
// configured maximum number of retained unknown fields.
var ErrTooManyUnknown = errors.New("unknown: too many unknown fields")

// Field is one retained, unrecognised field.
//
// Payload holds the raw payload bytes:
//   - Varint: the encoded varint bytes (with continuation bits),
//   - Bytes/Message: the length-prefixed payload, excluding the
//     length prefix itself.
//
// The slice is owned by the Field: callers must not mutate it, and
// Fields.Add copies any slice handed in.
type Field struct {
	Number  uint64
	Type    byte
	Payload []byte
}

// lengthPrefixed reports whether t carries a varint length prefix.
func lengthPrefixed(t byte) bool { return t == Bytes || t == Message }

// appendVarint appends v in base-128 little-endian encoding.
func appendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}
