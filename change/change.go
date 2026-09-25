// Package change defines base-table change records and their binary encoding.
package change

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

// Op is the kind of a base-table change.
type Op uint8

const (
	OpInsert Op = 1
	OpDelete Op = 2
	OpUpdate Op = 3
)

// Errors returned by Validate / Decode.
var (
	ErrBadOp          = errors.New("change: unknown op")
	ErrMissingGroup   = errors.New("change: group key missing")
	ErrNaNValue       = errors.New("change: value is NaN")
	ErrBodyIncomplete = errors.New("change: record body incomplete")
)

// Change is one base-table mutation. Group == "" is legal; GroupSet=false
// means the group field was absent and must be rejected.
type Change struct {
	Version  uint64
	Op       Op
	RKey     string // identifies the member record inside its group
	Group    string
	Value    float64
	OldGroup string
	OldValue float64
	GroupSet bool
	HasOld   bool // Update: old group/value are present
}

const flagGroupSet = 1 << 0
const flagHasOld = 1 << 1

// Validate checks structural and semantic validity of a change.
func (c Change) Validate() error {
	if c.Op != OpInsert && c.Op != OpDelete && c.Op != OpUpdate {
		return ErrBadOp
	}
	if !c.GroupSet {
		return ErrMissingGroup
	}
	if math.IsNaN(c.Value) {
		return ErrNaNValue
	}
	if c.Op == OpUpdate && (math.IsNaN(c.OldValue) || !c.HasOld) {
		return ErrNaNValue
	}
	return nil
}

func putStr(b *bytes.Buffer, s string) {
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(s)))
	b.Write(n[:])
	b.WriteString(s)
}

// Encode serializes a validated change into a self-describing byte payload.
func (c Change) Encode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	flags := byte(0)
	if c.GroupSet {
		flags |= flagGroupSet
	}
	if c.HasOld {
		flags |= flagHasOld
	}
	b.WriteByte(flags)
	var hdr [8]byte
	binary.BigEndian.PutUint64(hdr[:], c.Version)
	b.Write(hdr[:])
	b.WriteByte(byte(c.Op))
	var fv [8]byte
	binary.BigEndian.PutUint64(fv[:], math.Float64bits(c.Value))
	b.Write(fv[:])
	putStr(&b, c.Group)
	putStr(&b, c.RKey)
	if c.HasOld {
		binary.BigEndian.PutUint64(fv[:], math.Float64bits(c.OldValue))
		b.Write(fv[:])
		putStr(&b, c.OldGroup)
	}
	return b.Bytes(), nil
}

type reader struct{ b []byte }

func (r *reader) byte_(n int) ([]byte, error) {
	if len(r.b) < n {
		return nil, ErrBodyIncomplete
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out, nil
}

func (r *reader) str() (string, error) {
	h, err := r.byte_(2)
	if err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint16(h))
	p, err := r.byte_(n)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

// Decode parses a payload produced by Encode.
func Decode(p []byte) (Change, error) {
	r := &reader{b: p}
	var c Change
	fb, err := r.byte_(1)
	if err != nil {
		return c, err
	}
	c.GroupSet = fb[0]&flagGroupSet != 0
	c.HasOld = fb[0]&flagHasOld != 0
	vb, err := r.byte_(8)
	if err != nil {
		return c, err
	}
	c.Version = binary.BigEndian.Uint64(vb)
	ob, err := r.byte_(1)
	if err != nil {
		return c, err
	}
	c.Op = Op(ob[0])
	fb, err = r.byte_(8)
	if err != nil {
		return c, err
	}
	c.Value = math.Float64frombits(binary.BigEndian.Uint64(fb))
	if c.Group, err = r.str(); err != nil {
		return c, err
	}
	if c.RKey, err = r.str(); err != nil {
		return c, err
	}
	if c.HasOld {
		if fb, err = r.byte_(8); err != nil {
			return c, err
		}
		c.OldValue = math.Float64frombits(binary.BigEndian.Uint64(fb))
		if c.OldGroup, err = r.str(); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}
