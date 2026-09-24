// Package codec is the public API: signed varint encoding built on zz and vint.
package codec

import (
	"fmt"

	"ontology/vint"
	"ontology/zz"
)

// EncodeInt encodes one signed integer into a fresh byte slice.
func EncodeInt(v int64) []byte {
	var buf [10]byte
	n := vint.PutUvarint(buf[:], zz.Encode(v))
	return append([]byte(nil), buf[:n]...)
}

// DecodeInt decodes one signed integer, returning the value and the number
// of bytes consumed.
func DecodeInt(buf []byte) (int64, int, error) {
	u, n, err := vint.Uvarint(buf)
	if err != nil {
		return 0, 0, err
	}
	return zz.Decode(u), n, nil
}

// EncodeSlice encodes a slice back to back into one byte slice.
func EncodeSlice(vals []int64) []byte {
	var out []byte
	var buf [10]byte
	for _, v := range vals {
		n := vint.PutUvarint(buf[:], zz.Encode(v))
		out = append(out, buf[:n]...)
	}
	return out
}

// DecodeSlice decodes the whole buffer. It never mutates buf; on any failure
// it returns nil and an error naming the index of the offending element.
func DecodeSlice(buf []byte) ([]int64, error) {
	var out []int64
	for i := 0; len(buf) > 0; i++ {
		v, n, err := DecodeInt(buf)
		if err != nil {
			return nil, fmt.Errorf("codec: element %d: %w", i, err)
		}
		out = append(out, v)
		buf = buf[n:]
	}
	return out, nil
}

// SelfCheck round-trips vals and verifies the 10-byte encoding length bound.
func SelfCheck(vals []int64) error {
	for i, v := range vals {
		enc := EncodeInt(v)
		if len(enc) > 10 {
			return fmt.Errorf("codec: element %d: encoding longer than 10 bytes", i)
		}
		got, n, err := DecodeInt(enc)
		if err != nil {
			return fmt.Errorf("codec: element %d: %w", i, err)
		}
		if got != v || n != len(enc) {
			return fmt.Errorf("codec: element %d: roundtrip mismatch", i)
		}
	}
	return nil
}
