// Package cursor encodes and decodes opaque pagination cursors.
// A cursor carries a composite sort key (score, id) and a direction
// marker, protected by a CRC32 checksum.
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
)

// Sentinel decoding errors, distinguishable with errors.Is.
var (
	ErrTruncated = errors.New("cursor: incomplete fields")
	ErrDirection = errors.New("cursor: invalid direction marker")
	ErrChecksum  = errors.New("cursor: checksum mismatch")
)

// Direction is the paging direction recorded in a cursor.
type Direction byte

const (
	Forward  Direction = 0xC1
	Backward Direction = 0xC2
)

const (
	headerLen = 1 + 8 + 2 // direction + score bits + id length
	crcLen    = 4
	minLen    = headerLen + crcLen
)

// Cursor is the decoded form of an opaque cursor. Start is true when
// the cursor was decoded from an empty byte string, meaning "from the
// beginning" (forward) or "from the end" (backward).
type Cursor struct {
	Score float64
	ID    string
	Dir   Direction
	Start bool
}

// Encode renders an opaque cursor for the composite key (score, id).
func Encode(score float64, id string, dir Direction) []byte {
	b := make([]byte, 0, minLen+len(id))
	b = append(b, byte(dir))
	var bits [8]byte
	binary.BigEndian.PutUint64(bits[:], math.Float64bits(score))
	b = append(b, bits[:]...)
	var ln [2]byte
	binary.BigEndian.PutUint16(ln[:], uint16(len(id)))
	b = append(b, ln[:]...)
	b = append(b, id...)
	var crc [crcLen]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(b))
	return append(b, crc[:]...)
}

// Decode parses an opaque cursor. An empty input yields the Start
// cursor and no error. Validation order: field completeness, then
// direction marker, then checksum.
func Decode(b []byte) (Cursor, error) {
	if len(b) == 0 {
		return Cursor{Start: true}, nil
	}
	if len(b) < minLen {
		return Cursor{}, ErrTruncated
	}
	n := int(binary.BigEndian.Uint16(b[9:11]))
	if len(b) != minLen+n {
		return Cursor{}, ErrTruncated
	}
	dir := Direction(b[0])
	if dir != Forward && dir != Backward {
		return Cursor{}, ErrDirection
	}
	want := binary.BigEndian.Uint32(b[len(b)-crcLen:])
	if crc32.ChecksumIEEE(b[:len(b)-crcLen]) != want {
		return Cursor{}, ErrChecksum
	}
	return Cursor{
		Score: math.Float64frombits(binary.BigEndian.Uint64(b[1:9])),
		ID:    string(b[headerLen : headerLen+n]),
		Dir:   dir,
	}, nil
}
