// Package row defines join rows, their keys and length-prefixed encoding.
package row

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Row is one input record. Key is the equi-join key (empty string legal).
// Payload is opaque and preserved byte-for-byte; Seq is arrival order
// within a key on its own side, assigned by the stream.
type Row struct {
	Key     string
	Payload string
	Seq     int
}

var (
	// ErrShort is returned when a buffer ends inside a length prefix or body.
	ErrShort = errors.New("row: truncated encoding")
	// ErrBadLen is returned when a length prefix is negative.
	ErrBadLen = errors.New("row: negative length")
)

// Encoded size: uint32 keyLen, key, uint32 payloadLen, payload.
// Seq is not persisted: arrival order is implied by frame order.

// EncodedLen reports the encoded byte length of r.
func (r Row) EncodedLen() int {
	return 4 + len(r.Key) + 4 + len(r.Payload)
}

// Encode appends the encoding of r to dst and returns the new slice.
func (r Row) Encode(dst []byte) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(r.Key)))
	dst = append(dst, buf[:]...)
	dst = append(dst, r.Key...)
	binary.BigEndian.PutUint32(buf[:], uint32(len(r.Payload)))
	dst = append(dst, buf[:]...)
	dst = append(dst, r.Payload...)
	return dst
}

// Decode parses one row from the front of b, returning the row and the
// number of bytes consumed. Partial prefixes/body return ErrShort.
func Decode(b []byte) (Row, int, error) {
	if len(b) < 4 {
		return Row{}, 0, ErrShort
	}
	kl := int(binary.BigEndian.Uint32(b[:4]))
	if kl < 0 {
		return Row{}, 0, ErrBadLen
	}
	if len(b) < 4+kl+4 {
		return Row{}, 0, ErrShort
	}
	key := string(b[4 : 4+kl])
	pl := int(binary.BigEndian.Uint32(b[4+kl : 8+kl]))
	start := 8 + kl
	if len(b) < start+pl {
		return Row{}, 0, ErrShort
	}
	payload := string(b[start : start+pl])
	return Row{Key: key, Payload: payload}, start + pl, nil
}

// Pair is one emitted join result, in arrival order on both sides.
type Pair struct {
	Key     string
	Left    Row
	Right   Row
}

func (p Pair) String() string {
	return fmt.Sprintf("(%s: %s|%s)", p.Key, p.Left.Payload, p.Right.Payload)
}
