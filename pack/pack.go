// Package pack encodes and decodes fixed-schema records as fixed-width,
// fixed-offset little-endian byte strings.
package pack

import (
	"errors"

	"ontology/layout"
)

// Distinct, decidable sentinel errors. The three failure modes must never
// collapse into one another.
var (
	// ErrUnknownField: a value was supplied for a field not in the schema.
	ErrUnknownField = errors.New("pack: unknown field name")
	// ErrValueOutOfRange: a value does not fit the field's signed/unsigned width.
	ErrValueOutOfRange = errors.New("pack: value out of field range")
	// ErrBufferTooShort: the input buffer is shorter than the schema record.
	ErrBufferTooShort = errors.New("pack: buffer shorter than schema size")
)

// Pack encodes fields into a schema-sized byte string using little-endian
// encoding at each field's fixed offset. All inputs are validated before any
// output buffer is allocated, so a rejection returns (nil, error) and never a
// partially written record. Fields absent from the map encode as zero.
func Pack(s *layout.Schema, values map[string]int64) ([]byte, error) {
	fs := s.Fields()

	// Reject unknown names before touching anything.
	for name := range values {
		if !s.Has(name) {
			return nil, ErrUnknownField
		}
	}
	// Validate every value against its field's range up front.
	encoded := make([]uint64, len(fs))
	for i, f := range fs {
		v := values[f.Name]
		if !inRange(f, v) {
			return nil, ErrValueOutOfRange
		}
		encoded[i] = uint64(v)
	}

	// Only now allocate and write.
	buf := make([]byte, s.Size())
	for i, f := range fs {
		writeLE(buf[f.Offset:f.Offset+f.Width], encoded[i])
	}
	return buf, nil
}

// Unpack decodes a schema-sized buffer back to named int64 values. Signed
// fields are sign-extended and unsigned fields zero-extended. A short buffer
// fails wholesale with (nil, error).
func Unpack(s *layout.Schema, buf []byte) (map[string]int64, error) {
	if len(buf) < s.Size() {
		return nil, ErrBufferTooShort
	}
	fs := s.Fields()
	out := make(map[string]int64, len(fs))
	for _, f := range fs {
		u := readLE(buf[f.Offset : f.Offset+f.Width])
		if f.Signed {
			out[f.Name] = signExtend(u, f.Width)
		} else {
			out[f.Name] = int64(u)
		}
	}
	return out, nil
}

// inRange reports whether v fits a field of width w bytes.
func inRange(f layout.Field, v int64) bool {
	bits := uint(8 * f.Width)
	if f.Signed {
		min := int64(-1) << (bits - 1)
		max := int64(1)<<(bits-1) - 1 // valid for w<=8
		return v >= min && v <= max
	}
	// Unsigned 8-byte: every int64 bit pattern maps into [0, 2^64). For
	// narrower fields a negative int64 can never be a valid uvalue.
	if f.Width < 8 && v < 0 {
		return false
	}
	max := ^uint64(0) >> (64 - bits)
	return uint64(v) <= max
}

// writeLE writes the low w bytes of v in little-endian order, one byte at a
// time: the textbook offset-by-offset encoding.
func writeLE(dst []byte, v uint64) {
	for i := range dst {
		dst[i] = byte(v >> (8 * uint(i)))
	}
}

// readLE assembles w little-endian bytes into an unsigned integer.
func readLE(src []byte) uint64 {
	var u uint64
	for i, b := range src {
		u |= uint64(b) << (8 * uint(i))
	}
	return u
}

// signExtend widens a signed w-byte value held in u to int64.
func signExtend(u uint64, w int) int64 {
	if w == 8 {
		return int64(u) // already full-width two's complement
	}
	bits := uint(8 * w)
	if u&(uint64(1)<<(bits-1)) != 0 {
		return int64(u) - int64(uint64(1)<<bits)
	}
	return int64(u)
}
