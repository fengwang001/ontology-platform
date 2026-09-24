// Package event defines an append-only log event (sequence number + payload)
// and its length-prefixed, CRC32-protected frame encoding.
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Frame layout:

const (
	LenSize     = 4 // uint32 payload length
	SeqSize     = 8 // uint64 sequence number
	CRCSize     = 4 // uint32 CRC32
	FixedPrefix = LenSize + SeqSize
	FixedSuffix = CRCSize
)

var (
	ErrShortLength = errors.New("event: incomplete length prefix")
	ErrShortFrame  = errors.New("event: incomplete frame body")
	ErrCRC         = errors.New("event: crc mismatch")
)

// Event is one log record. Empty Payload is legal.
type Event struct {
	Seq     uint64
	Payload []byte
}

// EncodedLen returns the on-disk frame length of e.
func (e Event) EncodedLen() int {
	return FixedPrefix + len(e.Payload) + FixedSuffix
}

// Encode appends the frame for e to dst and returns the new slice.
func (e Event) Encode(dst []byte) []byte {
	total := e.EncodedLen()
	start := len(dst)
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:LenSize], uint32(len(e.Payload)))
	binary.BigEndian.PutUint64(out[LenSize:FixedPrefix], e.Seq)
	copy(out[FixedPrefix:], e.Payload)
	sum := crc32.ChecksumIEEE(out[:FixedPrefix+len(e.Payload)])
	binary.BigEndian.PutUint32(out[total-CRCSize:], sum)
	return append(dst, out[start:]...)
}

// Decode reads one frame from b. It returns the event and the number of bytes
// consumed, or a classifiable error when b holds a truncated/bad frame.
func Decode(b []byte) (Event, int, error) {
	if len(b) < LenSize {
		return Event{}, 0, ErrShortLength
	}
	plen := int(binary.BigEndian.Uint32(b[:LenSize]))
	total := FixedPrefix + plen + FixedSuffix
	if len(b) < total {
		return Event{}, 0, ErrShortFrame
	}
	frame := b[:total]
	want := binary.BigEndian.Uint32(frame[total-CRCSize:])
	if got := crc32.ChecksumIEEE(frame[:total-CRCSize]); got != want {
		return Event{}, 0, ErrCRC
	}
	seq := binary.BigEndian.Uint64(frame[LenSize:FixedPrefix])
	payload := make([]byte, plen)
	copy(payload, frame[FixedPrefix:FixedPrefix+plen])
	return Event{Seq: seq, Payload: payload}, total, nil
}
