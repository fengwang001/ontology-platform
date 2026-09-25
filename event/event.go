// Package event defines the log event (sequence number + payload)
// and its binary encoding.
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrTooShort is returned when decoding a buffer smaller than the
// fixed 8-byte sequence-number header.
var ErrTooShort = errors.New("event: encoded event shorter than 8-byte header")

// HeaderLen is the fixed length of the encoded sequence number.
const HeaderLen = 8

// Event is a single append-only log entry.
type Event struct {
	Seq     uint64
	Payload []byte
}

// Encode serializes e as seq (8-byte big-endian) followed by the raw
// payload. An empty payload is legal.
func Encode(e Event) []byte {
	buf := make([]byte, HeaderLen+len(e.Payload))
	binary.BigEndian.PutUint64(buf, e.Seq)
	copy(buf[HeaderLen:], e.Payload)
	return buf
}

// Decode parses a buffer produced by Encode.
func Decode(buf []byte) (Event, error) {
	if len(buf) < HeaderLen {
		return Event{}, fmt.Errorf("%w: got %d bytes", ErrTooShort, len(buf))
	}
	payload := make([]byte, len(buf)-HeaderLen)
	copy(payload, buf[HeaderLen:])
	return Event{Seq: binary.BigEndian.Uint64(buf), Payload: payload}, nil
}

// EncodedLen returns the encoded length of an event with the given
// payload size.
func EncodedLen(payloadLen int) int {
	return HeaderLen + payloadLen
}
