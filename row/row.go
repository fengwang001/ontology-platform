// Package row defines the input record of the aggregator: a group key
// plus a float64 value, and its binary codec used inside spill files.
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row is one input record: group key and numeric value.
type Row struct {
	Key string
	Val float64
}

// ErrMalformed is returned when a byte slice cannot be decoded into a Row.
var ErrMalformed = errors.New("row: malformed encoding")

// Encode serializes the row as uvarint(keyLen) | key | 8-byte LE value bits.
func (r Row) Encode() []byte {
	buf := make([]byte, 0, binary.MaxVarintLen64+len(r.Key)+8)
	buf = binary.AppendUvarint(buf, uint64(len(r.Key)))
	buf = append(buf, r.Key...)
	var bits [8]byte
	binary.LittleEndian.PutUint64(bits[:], math.Float64bits(r.Val))
	return append(buf, bits[:]...)
}

// Decode parses bytes produced by Encode.
func Decode(b []byte) (Row, error) {
	n, sz := binary.Uvarint(b)
	if sz <= 0 {
		return Row{}, ErrMalformed
	}
	rest := b[sz:]
	if uint64(len(rest)) < n+8 {
		return Row{}, ErrMalformed
	}
	key := string(rest[:n])
	val := math.Float64frombits(binary.LittleEndian.Uint64(rest[n : n+8]))
	return Row{Key: key, Val: val}, nil
}
