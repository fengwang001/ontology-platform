// Package event defines an append-log event and its byte encoding.
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the fixed event header: sequence number int64 (8 bytes).
const HeaderSize = 8

// Event is a single log record: a sequence number and an opaque payload.
// An empty payload is valid.
type Event struct {
	Seq     int64
	Payload []byte
}

// EncodedSize returns the on-disk size of the event encoding.
func (e Event) EncodedSize() int { return HeaderSize + len(e.Payload) }

// Encode appends the event encoding (seq little-endian followed by the
// payload) to dst and returns the new slice.
func (e Event) Encode(dst []byte) []byte {
	var buf [HeaderSize]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(e.Seq))
	dst = append(dst, buf[:]...)
	return append(dst, e.Payload...)
}

// ErrShortBuffer means a buffer was too small to hold a decoded event.
var ErrShortBuffer = errors.New("event: short buffer")

// Decode parses an event from buf, which must contain exactly HeaderSize +
// payload bytes. The returned payload aliases buf.
func Decode(buf []byte) (Event, error) {
	if len(buf) < HeaderSize {
		return Event{}, fmt.Errorf("%w: have %d need %d", ErrShortBuffer, len(buf), HeaderSize)
	}
	seq := int64(binary.LittleEndian.Uint64(buf[:HeaderSize]))
	payload := make([]byte, len(buf)-HeaderSize)
	copy(payload, buf[HeaderSize:])
	return Event{Seq: seq, Payload: payload}, nil
}
