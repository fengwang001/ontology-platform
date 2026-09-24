// Package change defines the change-stream records and their codec.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op is the operation carried by one change record.
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// BodyLen is the fixed self-describing record size used by journal frames.
const BodyLen = 128

// Change is one base-table mutation with a monotonically increasing version.
type Change struct {
	Ver      uint64
	Op       Op
	RecID    uint64
	Group    string
	Value    float64
	NewGroup string
	NewValue float64
}

var (
	// ErrBadOp is returned for an unknown operation.
	ErrBadOp = errors.New("change: unknown op")
	// ErrMissingGroup is returned for a missing/empty group key.
	ErrMissingGroup = errors.New("change: missing group")
	// ErrNaN is returned for NaN values.
	ErrNaN = errors.New("change: NaN value")
)

// Validate reports semantic input errors (empty group or NaN value).
func (c Change) Validate() error {
	if c.Op < Insert || c.Op > Update {
		return ErrBadOp
	}
	if c.Group == "" {
		return ErrMissingGroup
	}
	if math.IsNaN(c.Value) {
		return ErrNaN
	}
	if c.Op == Update && (c.NewGroup == "" || math.IsNaN(c.NewValue)) {
		if c.NewGroup == "" {
			return ErrMissingGroup
		}
		return ErrNaN
	}
	return nil
}

// Encode serialises c into exactly BodyLen bytes.
func (c Change) Encode() []byte {
	b := make([]byte, BodyLen)
	binary.LittleEndian.PutUint64(b[0:8], c.Ver)
	b[8] = byte(c.Op)
	binary.LittleEndian.PutUint64(b[9:17], c.RecID)
	binary.LittleEndian.PutUint64(b[17:25], math.Float64bits(c.Value))
	binary.LittleEndian.PutUint64(b[25:33], math.Float64bits(c.NewValue))
	g0 := []byte(c.Group)
	g1 := []byte(c.NewGroup)
	b[33] = byte(len(g0))
	copy(b[34:], g0)
	b[66] = byte(len(g1))
	copy(b[67:], g1)
	return b
}

// Decode parses BodyLen bytes produced by Encode.
func Decode(b []byte) (Change, error) {
	if len(b) != BodyLen {
		return Change{}, errors.New("change: bad body length")
	}
	c := Change{
		Ver:      binary.LittleEndian.Uint64(b[0:8]),
		Op:       Op(b[8]),
		RecID:    binary.LittleEndian.Uint64(b[9:17]),
		Value:    math.Float64frombits(binary.LittleEndian.Uint64(b[17:25])),
		NewValue: math.Float64frombits(binary.LittleEndian.Uint64(b[25:33])),
	}
	n := int(b[33])
	c.Group = string(b[34 : 34+n])
	n = int(b[66])
	c.NewGroup = string(b[67 : 67+n])
	if err := c.Validate(); err != nil {
		return Change{}, err
	}
	return c, nil
}
