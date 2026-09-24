// Package change defines the change record carried by the base-table
// change stream, plus its deterministic binary codec.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op is the kind of a base-table mutation.
type Op uint8

const (
	OpInsert Op = iota + 1
	OpDelete
	OpUpdate
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpDelete:
		return "delete"
	case OpUpdate:
		return "update"
	}
	return "unknown"
}

// Change is one base-table mutation. ID identifies the affected record;
// for updates Group/Value carry the new values (the view locates the old
// ones via ID). HasGroup distinguishes an empty group key (legal) from a
// missing one (rejected by the view).
type Change struct {
	Version  uint64
	Op       Op
	ID       uint64
	HasGroup bool
	Group    string
	Value    float64
}

// headerLen is op(1) + flags(1) + version(8) + id(8) + value(8) + grouplen(2).
const headerLen = 28

const flagHasGroup = 1

var (
	ErrBodyTooShort = errors.New("change: record body too short")
	ErrBadOp        = errors.New("change: unknown op")
	ErrGroupTooLong = errors.New("change: group key too long")
)

// Encode serializes c into its binary record body.
func Encode(c Change) ([]byte, error) {
	if len(c.Group) > math.MaxUint16 {
		return nil, ErrGroupTooLong
	}
	buf := make([]byte, headerLen+len(c.Group))
	buf[0] = byte(c.Op)
	if c.HasGroup {
		buf[1] = flagHasGroup
	}
	binary.LittleEndian.PutUint64(buf[2:10], c.Version)
	binary.LittleEndian.PutUint64(buf[10:18], c.ID)
	binary.LittleEndian.PutUint64(buf[18:26], math.Float64bits(c.Value))
	binary.LittleEndian.PutUint16(buf[26:28], uint16(len(c.Group)))
	copy(buf[headerLen:], c.Group)
	return buf, nil
}

// Decode parses a record body produced by Encode.
func Decode(buf []byte) (Change, error) {
	var c Change
	if len(buf) < headerLen {
		return c, ErrBodyTooShort
	}
	c.Op = Op(buf[0])
	switch c.Op {
	case OpInsert, OpDelete, OpUpdate:
	default:
		return c, ErrBadOp
	}
	c.HasGroup = buf[1]&flagHasGroup != 0
	c.Version = binary.LittleEndian.Uint64(buf[2:10])
	c.ID = binary.LittleEndian.Uint64(buf[10:18])
	c.Value = math.Float64frombits(binary.LittleEndian.Uint64(buf[18:26]))
	n := int(binary.LittleEndian.Uint16(buf[26:28]))
	if len(buf) != headerLen+n {
		return c, ErrBodyTooShort
	}
	c.Group = string(buf[headerLen:])
	return c, nil
}

// Equal reports whether two changes are field-by-field identical
// (Value compared bitwise). Used for duplicate-delivery detection.
func Equal(a, b Change) bool {
	return a.Version == b.Version && a.Op == b.Op && a.ID == b.ID &&
		a.HasGroup == b.HasGroup && a.Group == b.Group &&
		math.Float64bits(a.Value) == math.Float64bits(b.Value)
}

// Normalized returns c with -0 canonicalized to +0 so that sums and
// distinct-value maps treat both zeros identically.
func (c Change) Normalized() Change {
	if c.Value == 0 {
		c.Value = 0
	}
	return c
}
