// Package event defines an append-only log event and its length-prefixed,
// CRC-protected wire encoding.
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event is a single log record: a monotonically increasing sequence number
// plus an opaque (possibly empty) payload.
type Event struct {
	Seq     uint64
	Payload []byte
}

var crcTable = crc32.MakeTable(crc32.IEEE)

// MaxPayload bounds a single record so a corrupt length prefix cannot drive a
// giant allocation. The log format itself is otherwise unbounded.
const MaxPayload = 64 << 20 // 64 MiB

var (
	// ErrShortBuffer means a frame is not fully present in the buffer.
	ErrShortBuffer = errors.New("event: short buffer")
	// ErrBadLength means the declared length is implausible.
	ErrBadLength = errors.New("event: bad payload length")
	// ErrCRC means the stored checksum does not match the body.
	ErrCRC = errors.New("event: crc mismatch")
)

// EncodedLen returns the on-disk size of one record:
// len(4) + seq(8) + payload + crc(4).
func EncodedLen(payloadLen int) int {
	return 4 + 8 + payloadLen + 4
}

// AppendEncoded appends the framed event to dst and returns the new slice.
func AppendEncoded(dst []byte, e Event) []byte {
	var head [12]byte
	binary.LittleEndian.PutUint32(head[:4], uint32(len(e.Payload)))
	binary.LittleEndian.PutUint64(head[4:12], e.Seq)

	h := crc32.New(crcTable)
	h.Write(head[4:12])
	h.Write(e.Payload)
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], h.Sum32())

	dst = append(dst, head[:]...)
	dst = append(dst, e.Payload...)
	dst = append(dst, sum[:]...)
	return dst
}

// DecodeAt decodes one record starting at buf[0]. It returns the event and the
// number of bytes consumed.
func DecodeAt(buf []byte) (Event, int, error) {
	if len(buf) < 4 {
		return Event{}, 0, ErrShortBuffer
	}
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	if n > MaxPayload {
		return Event{}, 0, ErrBadLength
	}
	recLen := EncodedLen(n)
	if len(buf) < recLen {
		return Event{}, 0, ErrShortBuffer
	}
	seq := binary.LittleEndian.Uint64(buf[4:12])
	payload := buf[12 : 12+n]
	got := binary.LittleEndian.Uint32(buf[12+n : recLen])

	h := crc32.New(crcTable)
	h.Write(buf[4:12])
	h.Write(payload)
	if h.Sum32() != got {
		return Event{}, 0, ErrCRC
	}

	e := Event{Seq: seq, Payload: make([]byte, n)}
	copy(e.Payload, payload)
	return e, recLen, nil
}
