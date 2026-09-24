// Package event defines the wire encoding of a single log event:
// a uint32 payload length, the payload bytes, and a CRC32 checksum.
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

const (
	// LenSize is the byte size of the payload length prefix.
	LenSize = 4
	// CRCSize is the byte size of the trailing checksum.
	CRCSize = 4
	// MaxPayload bounds a plausible length prefix; anything larger is
	// treated as corruption rather than an allocation request.
	MaxPayload = 1 << 20
)

var (
	ErrShortLengthPrefix = errors.New("event: length prefix incomplete")
	ErrShortBody         = errors.New("event: body incomplete")
	ErrCRCMismatch       = errors.New("event: crc mismatch")
	ErrBadLength         = errors.New("event: length prefix out of range")
)

// Event is a sequenced payload. Seq is implicit on disk (derived from the
// segment header plus position) and attached in memory by readers.
type Event struct {
	Seq     uint64
	Payload []byte
}

// EncodedSize returns the on-disk size of a record with the given payload.
func EncodedSize(payloadLen int) int { return LenSize + payloadLen + CRCSize }

func checksum(lenAndPayload []byte) uint32 { return crc32.ChecksumIEEE(lenAndPayload) }

// Encode serializes a payload into a self-contained record.
func Encode(payload []byte) []byte {
	buf := make([]byte, EncodedSize(len(payload)))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)))
	copy(buf[LenSize:], payload)
	binary.BigEndian.PutUint32(buf[LenSize+len(payload):], checksum(buf[:LenSize+len(payload)]))
	return buf
}

// Decode reads exactly one record from r. It returns the payload and the
// number of bytes consumed. io.EOF (with n == 0) means a clean end at a
// record boundary; any other error is one of the package sentinels.
func Decode(r io.Reader) (payload []byte, n int, err error) {
	var lenBuf [LenSize]byte
	nr, err := io.ReadFull(r, lenBuf[:])
	n += nr
	if err == io.EOF {
		return nil, n, io.EOF
	}
	if err != nil {
		return nil, n, ErrShortLengthPrefix
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length > MaxPayload {
		return nil, n, ErrBadLength
	}
	body := make([]byte, int(length)+CRCSize)
	nr, err = io.ReadFull(r, body)
	n += nr
	if err != nil {
		if nr < int(length) {
			return nil, n, ErrShortBody
		}
		return nil, n, ErrCRCMismatch
	}
	head := make([]byte, 0, LenSize+int(length))
	head = append(head, lenBuf[:]...)
	head = append(head, body[:length]...)
	if checksum(head) != binary.BigEndian.Uint32(body[length:]) {
		return nil, n, ErrCRCMismatch
	}
	return body[:length], n, nil
}
