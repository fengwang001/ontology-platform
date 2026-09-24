// Package event defines the append-only log event and its length-prefixed,
// CRC-protected wire frame.
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event is one log record. Payload may be empty but must be non-nil-safe;
// a nil payload and an empty payload encode identically.
type Event struct {
	Seq     uint64
	Payload []byte
}

// Frame layout: [4-byte big-endian payload length][payload][4-byte CRC32].
const (
	LenSize   = 4
	CRCSize   = 4
	FrameOver = LenSize + CRCSize
)

var (
	// ErrShortFrame means fewer bytes than the frame declares are available.
	ErrShortFrame = errors.New("event: truncated frame")
	// ErrCRCMismatch means the payload does not match its trailing CRC.
	ErrCRCMismatch = errors.New("event: crc mismatch")
)

// EncodedLen returns the on-disk size of e.
func (e Event) EncodedLen() int {
	return FrameOver + len(e.Payload)
}

// Encode appends the frame for e to dst and returns the new slice.
func (e Event) Encode(dst []byte) []byte {
	var buf [LenSize]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(e.Payload)))
	dst = append(dst, buf[:]...)
	dst = append(dst, e.Payload...)
	var crc [CRCSize]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(e.Payload))
	return append(dst, crc[:]...)
}

// DecodeAt decodes one frame starting at data[off:].
// It returns the event, the offset just past the frame, and an error.
func DecodeAt(data []byte, off int) (Event, int, error) {
	if len(data)-off < LenSize {
		return Event{}, off, ErrShortFrame
	}
	n := int(binary.BigEndian.Uint32(data[off:]))
	end := off + FrameOver + n
	if end > len(data) || end < off {
		return Event{}, off, ErrShortFrame
	}
	payload := data[off+LenSize : off+LenSize+n]
	got := binary.BigEndian.Uint32(data[off+LenSize+n:])
	if got != crc32.ChecksumIEEE(payload) {
		return Event{}, off, ErrCRCMismatch
	}
	return Event{Seq: 0, Payload: append([]byte(nil), payload...)}, end, nil
}
