// Package row defines an input row (group key + value) and its binary codec.
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row is one input record grouped by Key.
type Row struct {
	Key   string
	Value float64
}

// ErrShort means the buffer is too short to decode a row.
var ErrShort = errors.New("row: buffer too short")

// Encode serializes a row as keyLen u32 | key | value f64.
func Encode(r Row) []byte {
	buf := make([]byte, 4+len(r.Key)+8)
	binary.LittleEndian.PutUint32(buf, uint32(len(r.Key)))
	copy(buf[4:], r.Key)
	binary.LittleEndian.PutUint64(buf[4+len(r.Key):], math.Float64bits(r.Value))
	return buf
}

// Decode parses a buffer produced by Encode.
func Decode(buf []byte) (Row, error) {
	if len(buf) < 12 {
		return Row{}, ErrShort
	}
	n := binary.LittleEndian.Uint32(buf)
	end := uint64(4) + uint64(n) + 8
	if uint64(len(buf)) < end || n > uint32(len(buf)-12) {
		return Row{}, ErrShort
	}
	return Row{
		Key:   string(buf[4 : 4+n]),
		Value: math.Float64frombits(binary.LittleEndian.Uint64(buf[4+n:])),
	}, nil
}
