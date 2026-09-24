// Package change defines base-table change records and their payload encoding.
//
// A Change is one of Insert, Delete or Update. An Update is equivalent to
// deleting the (OldGroup, OldValue) side and inserting the (Group, Value) side.
// Version numbers are required to be monotonically increasing on a stream.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op is the change operation type.
type Op uint8

const (
	OpInsert Op = 1
	OpDelete Op = 2
	OpUpdate Op = 3
)

// Validation errors returned by Validate.
var (
	ErrMissingGroup = errors.New("change: missing group key")
	ErrNaNValue     = errors.New("change: NaN value")
	ErrBadOp        = errors.New("change: unknown op")
)

// Change is a single base-table change.
//
// Group/Value describe the new tuple (Insert), removed tuple (Delete) or new
// side (Update). OldGroup/OldValue describe the removed side of an Update.
// The GroupOK flags distinguish the empty-string group (valid) from a missing
// key (rejected).
type Change struct {
	Version    uint64
	Op         Op
	Group      string
	GroupOK    bool
	Value      float64
	OldGroup   string
	OldGroupOK bool
	OldValue   float64
}

// Insert builds an insertion change.
func Insert(version uint64, group string, value float64) Change {
	return Change{Version: version, Op: OpInsert, Group: group, GroupOK: true, Value: value}
}

// Delete builds a deletion change.
func Delete(version uint64, group string, value float64) Change {
	return Change{Version: version, Op: OpDelete, Group: group, GroupOK: true, Value: value}
}

// Update builds a move/update change from old to new.
func Update(version uint64, newGroup string, newValue float64, oldGroup string, oldValue float64) Change {
	return Change{
		Version: version, Op: OpUpdate,
		Group: newGroup, GroupOK: true, Value: newValue,
		OldGroup: oldGroup, OldGroupOK: true, OldValue: oldValue,
	}
}

// Validate checks group presence and value finiteness rules.
func (c Change) Validate() error {
	if c.Op < OpInsert || c.Op > OpUpdate {
		return ErrBadOp
	}
	if !c.GroupOK || (c.Op == OpUpdate && !c.OldGroupOK) {
		return ErrMissingGroup
	}
	if math.IsNaN(c.Value) || (c.Op == OpUpdate && math.IsNaN(c.OldValue)) {
		return ErrNaNValue
	}
	return nil
}

// Removed returns the group/value removed by the change, if any.
func (c Change) Removed() (group string, value float64, ok bool) {
	switch c.Op {
	case OpDelete:
		return c.Group, c.Value, true
	case OpUpdate:
		return c.OldGroup, c.OldValue, true
	default:
		return "", 0, false
	}
}

// Added returns the group/value inserted by the change, if any.
func (c Change) Added() (group string, value float64, ok bool) {
	switch c.Op {
	case OpInsert, OpUpdate:
		return c.Group, c.Value, true
	default:
		return "", 0, false
	}
}

const missingMarker = uint32(0xFFFFFFFF)

func appendString(b []byte, s string, ok bool) []byte {
	if !ok {
		return binary.BigEndian.AppendUint32(b, missingMarker)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

func readString(b []byte) (string, bool, []byte, error) {
	if len(b) < 4 {
		return "", false, nil, errors.New("change: short string header")
	}
	n := binary.BigEndian.Uint32(b)
	b = b[4:]
	if n == missingMarker {
		return "", false, b, nil
	}
	if int(n) > len(b) {
		return "", false, nil, errors.New("change: short string body")
	}
	return string(b[:n]), true, b[n:], nil
}

// MarshalPayload encodes the change into a self-describing binary payload.
func MarshalPayload(c Change) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	b := make([]byte, 0, 32)
	b = binary.BigEndian.AppendUint64(b, c.Version)
	b = append(b, byte(c.Op))
	b = appendString(b, c.Group, c.GroupOK)
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.Value))
	if c.Op == OpUpdate {
		b = appendString(b, c.OldGroup, c.OldGroupOK)
		b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.OldValue))
	}
	return b, nil
}

// UnmarshalPayload decodes a payload produced by MarshalPayload.
func UnmarshalPayload(b []byte) (Change, error) {
	var c Change
	if len(b) < 13 {
		return c, errors.New("change: payload too short")
	}
	c.Version = binary.BigEndian.Uint64(b)
	c.Op = Op(b[8])
	var err error
	b = b[9:]
	if c.Group, c.GroupOK, b, err = readString(b); err != nil {
		return c, err
	}
	if len(b) < 8 {
		return c, errors.New("change: short value")
	}
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(b))
	b = b[8:]
	if c.Op == OpUpdate {
		if c.OldGroup, c.OldGroupOK, b, err = readString(b); err != nil {
			return c, err
		}
		if len(b) < 8 {
			return c, errors.New("change: short old value")
		}
		c.OldValue = math.Float64frombits(binary.BigEndian.Uint64(b))
		b = b[8:]
	}
	if len(b) != 0 {
		return c, errors.New("change: trailing payload bytes")
	}
	return c, c.Validate()
}
