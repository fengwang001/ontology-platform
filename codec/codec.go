// Package codec is the public API: signed-varint encoding built on
// zz (zigzag) and vint (unsigned varint).
package codec

import (
	"errors"
	"fmt"

	"ontology/vint"
	"ontology/zz"
)

// Re-exported sentinels so callers can classify every failure.
var (
	ErrIncomplete = vint.ErrIncomplete
	ErrNonMinimal = vint.ErrNonMinimal
	ErrOverflow   = vint.ErrOverflow
)

// EncodeInt encodes one int64 in minimal form.
func EncodeInt(v int64) []byte {
	var buf [10]byte
	n := vint.PutUvarint(buf[:], zz.Encode(v))
	return append([]byte(nil), buf[:n]...)
}

// DecodeInt decodes one int64 from the front of buf.
func DecodeInt(buf []byte) (int64, error) {
	u, _, err := vint.Uvarint(buf)
	if err != nil {
		return 0, err
	}
	return zz.Decode(u), nil
}

// EncodeSlice encodes a slice back to back into one buffer.
func EncodeSlice(vs []int64) []byte {
	var out []byte
	var buf [10]byte
	for _, v := range vs {
		n := vint.PutUvarint(buf[:], zz.Encode(v))
		out = append(out, buf[:n]...)
	}
	return out
}

// DecodeSlice decodes a whole buffer. Any failure rejects the entire
// input: the result is nil and buf is never written to.
func DecodeSlice(buf []byte) ([]int64, error) {
	var out []int64
	for len(buf) > 0 {
		u, n, err := vint.Uvarint(buf)
		if err != nil {
			return nil, fmt.Errorf("codec: element %d: %w", len(out), err)
		}
		out = append(out, zz.Decode(u))
		buf = buf[n:]
	}
	return out, nil
}

// SelfCheck encodes vals, verifies the 10-bytes-per-value bound,
// decodes, and compares element by element.
func SelfCheck(vals []int64) error {
	enc := EncodeSlice(vals)
	if len(enc) > 10*len(vals) {
		return errors.New("codec: encoding exceeds 10 bytes per value")
	}
	dec, err := DecodeSlice(enc)
	if err != nil {
		return err
	}
	if len(dec) != len(vals) {
		return errors.New("codec: decoded length mismatch")
	}
	for i, v := range vals {
		if dec[i] != v {
			return fmt.Errorf("codec: element %d: got %d want %d", i, dec[i], v)
		}
	}
	return nil
}
