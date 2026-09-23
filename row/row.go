// Package row defines the input row (group key + value) and its codec.
package row

import (
	"encoding/binary"
	"errors"
	"math"
)

// Row is one input record: a group key and a numeric value.
type Row struct {
	Key string
	Val float64
}

// ErrMalformed is returned when decoding a truncated or invalid encoding.
var ErrMalformed = errors.New("row: malformed encoding")

const keyLenSize = 4

// Encode serializes r as: keyLen uint32 LE | key bytes | value bits uint64 LE.
// A zero value is normalized to +0.0 so that ±0 compare and hash equal.
func Encode(r Row) []byte {
	v := r.Val
	if v == 0 {
		v = 0 // normalize -0.0 to +0.0
	}
	buf := make([]byte, keyLenSize+len(r.Key)+8)
	binary.LittleEndian.PutUint32(buf, uint32(len(r.Key)))
	copy(buf[keyLenSize:], r.Key)
	binary.LittleEndian.PutUint64(buf[keyLenSize+len(r.Key):], math.Float64bits(v))
	return buf
}

// Decode parses data produced by Encode.
func Decode(data []byte) (Row, error) {
	if len(data) < keyLenSize {
		return Row{}, ErrMalformed
	}
	n := int(binary.LittleEndian.Uint32(data))
	if len(data) != keyLenSize+n+8 {
		return Row{}, ErrMalformed
	}
	key := string(data[keyLenSize : keyLenSize+n])
	val := math.Float64frombits(binary.LittleEndian.Uint64(data[keyLenSize+n:]))
	return Row{Key: key, Val: val}, nil
}
