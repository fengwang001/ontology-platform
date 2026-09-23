// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import (
	"errors"
	"io"
)

const (
	Magic0  byte = 'L'
	Magic1  byte = 'Z'
	Magic2  byte = '7'
	Version byte = 1

	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3

	MinMatch = 3
	MaxMatch = 258
)

// ErrVarInt is returned when a varint exceeds 10 bytes or overflows uint64.
var ErrVarInt = errors.New("wire: varint too long or overflow")

// PutVarInt appends an unsigned LEB128 encoding of v to b.
func PutVarInt(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadVarInt reads an unsigned LEB128 from r. io.ErrUnexpectedEOF means the
// stream ended mid-integer; ErrVarInt means an illegal (>10B/overflow) encoding.
func ReadVarInt(r io.ByteReader) (uint64, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && i > 0 {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if i == 9 && c > 1 {
			return 0, ErrVarInt
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, nil
		}
	}
	return 0, ErrVarInt
}

// WriteHeader writes the stream header.
func WriteHeader(w io.Writer, winSize, maxChain uint64) error {
	hdr := []byte{Magic0, Magic1, Magic2, Version}
	hdr = PutVarInt(hdr, winSize)
	hdr = PutVarInt(hdr, maxChain)
	_, err := w.Write(hdr)
	return err
}

// WriteLiteral writes one literal run.
func WriteLiteral(w io.Writer, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	b := []byte{TagLiteral}
	b = PutVarInt(b, uint64(len(data)))
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// WriteMatch writes one back-reference.
func WriteMatch(w io.Writer, dist, length uint64) error {
	b := []byte{TagMatch}
	b = PutVarInt(b, dist)
	b = PutVarInt(b, length)
	_, err := w.Write(b)
	return err
}

// WriteFlush writes a flush marker.
func WriteFlush(w io.Writer) error {
	_, err := w.Write([]byte{TagFlush})
	return err
}

// WriteEnd writes the stream trailer.
func WriteEnd(w io.Writer, total, checksum uint64) error {
	b := []byte{TagEnd}
	b = PutVarInt(b, total)
	b = PutVarInt(b, checksum)
	_, err := w.Write(b)
	return err
}
