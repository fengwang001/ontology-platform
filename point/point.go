// Package point defines a time-series sample and its binary encoding.
package point

import (
	"encoding/binary"
	"errors"
	"math"
)

// Point is a single sample: nanosecond timestamp and float64 value.
type Point struct {
	TS    int64
	Value float64
}

// Bucket is one aligned output bucket.
type Bucket struct {
	Start int64
	Value float64
	Count int64
}

// EncodedSize is the fixed wire size of a Point: 8 bytes ts + 8 bytes value.
const EncodedSize = 16

// ErrShortBuffer means the buffer is too small to hold an encoded point.
var ErrShortBuffer = errors.New("point: buffer too short")

// Encode writes the point as 16 little-endian bytes into dst.
func (p Point) Encode(dst []byte) error {
	if len(dst) < EncodedSize {
		return ErrShortBuffer
	}
	binary.LittleEndian.PutUint64(dst[0:8], uint64(p.TS))
	binary.LittleEndian.PutUint64(dst[8:16], math.Float64bits(p.Value))
	return nil
}

// Decode reads a Point from 16 little-endian bytes.
func Decode(src []byte) (Point, error) {
	if len(src) < EncodedSize {
		return Point{}, ErrShortBuffer
	}
	return Point{
		TS:    int64(binary.LittleEndian.Uint64(src[0:8])),
		Value: math.Float64frombits(binary.LittleEndian.Uint64(src[8:16])),
	}, nil
}

// IsNaN reports whether the point carries a NaN value.
func (p Point) IsNaN() bool { return math.IsNaN(p.Value) }
