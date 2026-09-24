// Package change defines the base-table change stream record and its
// length-prefixed, CRC-independent body encoding.
package change

import (
	"encoding/binary"
	"errors"
	"math"
)

// Op identifies the base-table operation.
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

// Stream-level decidable errors.
var (
	ErrMissingKey    = errors.New("change: missing group key")
	ErrNaN           = errors.New("change: NaN value is not allowed")
	ErrStaleVersion  = errors.New("change: version older than applied version")
	ErrOutOfOrder    = errors.New("change: version is not lastVersion+1")
	ErrUnknownRecord = errors.New("change: unknown record id")
	ErrBadEncoding   = errors.New("change: malformed encoding")
)

// Change is one base-table mutation. KeyPresent==false means the key is
// absent (rejected); Key=="" with KeyPresent==true is the legal empty key.
// For Update, OldKey/OldVal describe the previous image and NewKey/Key/Val
// the new image.
type Change struct {
	Version    uint64
	ID         uint64
	Op         Op
	KeyPresent bool
	Key        string
	Val        float64
	OldPresent bool
	OldKey     string
	OldVal     float64
}

const (
	flagKey    = 1 << 0
	flagOldKey = 1 << 1
)

// Normalize maps -0.0 to +0.0 so signed zeros compare and hash as equal.
func Normalize(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// Validate checks structural and value constraints. Version ordering is the
// view's responsibility and is not checked here.
func (c Change) Validate() error {
	switch c.Op {
	case Insert, Delete:
	case Update:
		if !c.OldPresent {
			return ErrMissingKey
		}
	default:
		return ErrBadEncoding
	}
	if !c.KeyPresent {
		return ErrMissingKey
	}
	if math.IsNaN(c.Val) || math.IsNaN(c.OldVal) {
		return ErrNaN
	}
	return nil
}

func putStr(b []byte, s string) []byte {
	var lb [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lb[:], uint64(len(s)))
	b = append(b, lb[:n]...)
	return append(b, s...)
}

func putF64(b []byte, v float64) []byte {
	var fb [8]byte
	binary.BigEndian.PutUint64(fb[:], math.Float64bits(Normalize(v)))
	return append(b, fb[:]...)
}

// Marshal encodes the validated change body (without frame/CRC).
func Marshal(c Change) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	b := []byte{byte(c.Op)}
	var vb [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(vb[:], c.Version)
	b = append(b, vb[:n]...)
	n = binary.PutUvarint(vb[:], c.ID)
	b = append(b, vb[:n]...)
	var flags byte
	if c.KeyPresent {
		flags |= flagKey
	}
	if c.Op == Update && c.OldPresent {
		flags |= flagOldKey
	}
	b = append(b, flags)
	b = putStr(b, c.Key)
	b = putF64(b, c.Val)
	if c.Op == Update {
		b = putStr(b, c.OldKey)
		b = putF64(b, c.OldVal)
	}
	return b, nil
}

func takeStr(b []byte) (string, []byte, error) {
	l, k := binary.Uvarint(b)
	if k <= 0 || uint64(k)+l > uint64(len(b)) {
		return "", nil, ErrBadEncoding
	}
	return string(b[k : uint64(k)+l]), b[uint64(k)+l:], nil
}

func takeF64(b []byte) (float64, []byte, error) {
	if len(b) < 8 {
		return 0, nil, ErrBadEncoding
	}
	v := math.Float64frombits(binary.BigEndian.Uint64(b))
	return v, b[8:], nil
}

// Unmarshal decodes a body produced by Marshal.
func Unmarshal(body []byte) (Change, error) {
	var c Change
	if len(body) < 2 {
		return c, ErrBadEncoding
	}
	c.Op = Op(body[0])
	b := body[1:]
	v, k := binary.Uvarint(b)
	if k <= 0 {
		return c, ErrBadEncoding
	}
	c.Version = v
	b = b[k:]
	if v, k = binary.Uvarint(b); k <= 0 {
		return c, ErrBadEncoding
	}
	c.ID = v
	b = b[k:]
	if len(b) < 1 {
		return c, ErrBadEncoding
	}
	flags := b[0]
	b = b[1:]
	c.KeyPresent = flags&flagKey != 0
	c.OldPresent = flags&flagOldKey != 0
	var err error
	if c.Key, b, err = takeStr(b); err != nil {
		return c, err
	}
	if c.Val, b, err = takeF64(b); err != nil {
		return c, err
	}
	if c.Op == Update {
		if c.OldKey, b, err = takeStr(b); err != nil {
			return c, err
		}
		if c.OldVal, b, err = takeF64(b); err != nil {
			return c, err
		}
	}
	if len(b) != 0 {
		return c, ErrBadEncoding
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
