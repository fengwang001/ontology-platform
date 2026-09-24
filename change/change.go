// Package change defines the change record and its binary codec.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op is the kind of a base-table change.
type Op byte

const (
	OpInsert Op = iota + 1
	OpDelete
	OpUpdate
)

// ErrInvalid marks a malformed encoded record.
var ErrInvalid = errors.New("change: invalid record")

// Change is one base-table mutation. Version is assigned by the upstream
// stream and must be monotonically increasing. An empty Group is legal;
// HasGroup=false means the key is missing and the change must be rejected.
type Change struct {
	Version  uint64
	Op       Op
	ID       string
	Group    string
	HasGroup bool
	Value    float64
}

// fixedSize: version(8) op(1) hasGroup(1) idLen(2) groupLen(2) value(8).
const fixedSize = 8 + 1 + 1 + 2 + 2 + 8

// Encode serializes the change into a self-contained byte slice.
func (c Change) Encode() []byte {
	b := make([]byte, fixedSize+len(c.ID)+len(c.Group))
	binary.BigEndian.PutUint64(b[0:8], c.Version)
	b[8] = byte(c.Op)
	if c.HasGroup {
		b[9] = 1
	}
	binary.BigEndian.PutUint16(b[10:12], uint16(len(c.ID)))
	binary.BigEndian.PutUint16(b[12:14], uint16(len(c.Group)))
	binary.BigEndian.PutUint64(b[14:22], math.Float64bits(c.Value))
	off := fixedSize
	copy(b[off:], c.ID)
	off += len(c.ID)
	copy(b[off:], c.Group)
	return b
}

// Decode parses a buffer produced by Encode.
func Decode(b []byte) (Change, error) {
	var c Change
	if len(b) < fixedSize {
		return c, ErrInvalid
	}
	c.Version = binary.BigEndian.Uint64(b[0:8])
	c.Op = Op(b[8])
	c.HasGroup = b[9] == 1
	idLen := int(binary.BigEndian.Uint16(b[10:12]))
	grpLen := int(binary.BigEndian.Uint16(b[12:14]))
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(b[14:22]))
	if len(b) != fixedSize+idLen+grpLen {
		return c, ErrInvalid
	}
	c.ID = string(b[fixedSize : fixedSize+idLen])
	c.Group = string(b[fixedSize+idLen:])
	if c.Op < OpInsert || c.Op > OpUpdate {
		return c, ErrInvalid
	}
	return c, nil
}
