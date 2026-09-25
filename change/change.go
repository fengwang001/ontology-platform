// Package change defines the base-table change stream records and their
// length-prefixed binary encoding used by the write-ahead journal.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op is the kind of base-table change.
type Op uint8

const (
	Insert Op = iota + 1
	Delete
	Update
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	default:
		return "invalid"
	}
}

// Change is one versioned base-table mutation.
//
// For Insert/Delete, Key identifies the group and Value the record value.
// For Update, OldKey/OldValue describe the previous record and Key/Value the
// new one (a record may move between groups).
type Change struct {
	Version  uint64
	Op       Op
	Key      string
	Value    float64
	OldKey   string
	OldValue float64
}

// Sentinel validation errors.
var (
	ErrInvalidOp  = errors.New("change: invalid operation")
	ErrMissingKey = errors.New("change: missing group key")
	ErrNaN        = errors.New("change: NaN value")
)

func validFloat(v float64) bool { return !math.IsNaN(v) }

// Valid checks fields independent of version ordering.
func (c Change) Valid() error {
	if c.Op != Insert && c.Op != Delete && c.Op != Update {
		return ErrInvalidOp
	}
	if c.Key == "" {
		return ErrMissingKey
	}
	if !validFloat(c.Value) {
		return ErrNaN
	}
	if c.Op == Update {
		if c.OldKey == "" {
			return ErrMissingKey
		}
		if !validFloat(c.OldValue) {
			return ErrNaN
		}
	}
	return nil
}

// EncodedLen is the number of bytes Encode writes.
func (c Change) EncodedLen() int {
	n := 8 + 1 + 4 + len(c.Key) + 8
	if c.Op == Update {
		n += 4 + len(c.OldKey) + 8
	}
	return n
}

// Encode appends the self-delimited record to dst.
func (c Change) Encode(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint64(dst, c.Version)
	dst = append(dst, byte(c.Op))
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(c.Key)))
	dst = append(dst, c.Key...)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(c.Value))
	dst = append(dst, buf[:]...)
	if c.Op == Update {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(c.OldKey)))
		dst = append(dst, c.OldKey...)
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(c.OldValue))
		dst = append(dst, buf[:]...)
	}
	return dst
}

func short(b []byte, n int) bool { return len(b) < n }

// Decode parses one record previously produced by Encode.
func Decode(b []byte) (Change, int, error) {
	var c Change
	if short(b, 9) {
		return c, 0, errors.New("change: short record")
	}
	c.Version = binary.LittleEndian.Uint64(b[:8])
	c.Op = Op(b[8])
	off := 9
	kl := int(binary.LittleEndian.Uint32(b[off : off+4]))
	off += 4
	if short(b, off+kl+8) {
		return c, 0, errors.New("change: short key/value")
	}
	c.Key = string(b[off : off+kl])
	off += kl
	c.Value = math.Float64frombits(binary.LittleEndian.Uint64(b[off : off+8]))
	off += 8
	if c.Op == Update {
		if short(b, off+4) {
			return c, 0, errors.New("change: short old-key length")
		}
		ol := int(binary.LittleEndian.Uint32(b[off : off+4]))
		off += 4
		if short(b, off+ol+8) {
			return c, 0, errors.New("change: short old key/value")
		}
		c.OldKey = string(b[off : off+ol])
		off += ol
		c.OldValue = math.Float64frombits(binary.LittleEndian.Uint64(b[off : off+8]))
		off += 8
	}
	if err := c.Valid(); err != nil {
		return c, 0, err
	}
	return c, off, nil
}
