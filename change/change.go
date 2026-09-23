// Package change defines the change-stream record type and its binary payload
// encoding. The journal package wraps these payloads with a length prefix and
// a CRC32 checksum.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op identifies the kind of base-table mutation.
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// Change is one base-table mutation. Group uses *string so that an empty
// string group ("") is distinguishable from a missing group (nil), which the
// view must reject.
type Change struct {
	Op      Op
	Version uint64
	ID      string
	Group   *string
	Value   float64

	// NewGroup/NewValue apply only to Update.
	NewGroup *string
	NewValue float64
}

var (
	ErrMalformed = errors.New("change: malformed payload")
	ErrUnknownOp = errors.New("change: unknown op")
)

// EncodePayload serializes a change into a self-describing byte payload.
func EncodePayload(c Change) []byte {
	b := make([]byte, 0, 64)
	b = append(b, byte(c.Op))
	b = binary.BigEndian.AppendUint64(b, c.Version)
	b = appendString(b, c.ID)
	b = appendNullableString(b, c.Group)
	b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.Value))
	if c.Op == Update {
		b = appendNullableString(b, c.NewGroup)
		b = binary.BigEndian.AppendUint64(b, math.Float64bits(c.NewValue))
	}
	return b
}

// DecodePayload parses a payload produced by EncodePayload.
func DecodePayload(p []byte) (Change, error) {
	var c Change
	if len(p) < 1 {
		return c, ErrMalformed
	}
	c.Op = Op(p[0])
	if c.Op < Insert || c.Op > Update {
		return c, ErrUnknownOp
	}
	r := decoder{p: p[1:]}
	var ok bool
	if c.Version, ok = r.u64(); !ok {
		return c, ErrMalformed
	}
	if c.ID, ok = r.str(); !ok {
		return c, ErrMalformed
	}
	if c.Group, ok = r.nstr(); !ok {
		return c, ErrMalformed
	}
	v, ok := r.u64()
	if !ok {
		return c, ErrMalformed
	}
	c.Value = math.Float64frombits(v)
	if c.Op == Update {
		if c.NewGroup, ok = r.nstr(); !ok {
			return c, ErrMalformed
		}
		if v, ok = r.u64(); !ok {
			return c, ErrMalformed
		}
		c.NewValue = math.Float64frombits(v)
	}
	if len(r.p) != 0 {
		return c, ErrMalformed
	}
	return c, nil
}

type decoder struct{ p []byte }

func (d *decoder) take(n int) ([]byte, bool) {
	if len(d.p) < n {
		return nil, false
	}
	v := d.p[:n]
	d.p = d.p[n:]
	return v, true
}

func (d *decoder) u64() (uint64, bool) {
	b, ok := d.take(8)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}

func (d *decoder) str() (string, bool) {
	b, ok := d.take(2)
	if !ok {
		return "", false
	}
	n := int(binary.BigEndian.Uint16(b))
	s, ok := d.take(n)
	if !ok {
		return "", false
	}
	return string(s), true
}

func (d *decoder) nstr() (*string, bool) {
	b, ok := d.take(1)
	if !ok {
		return nil, false
	}
	if b[0] == 0 {
		return nil, true
	}
	s, ok := d.str()
	if !ok {
		return nil, false
	}
	return &s, true
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func appendNullableString(b []byte, s *string) []byte {
	if s == nil {
		return append(b, 0)
	}
	b = append(b, 1)
	return appendString(b, *s)
}
