// Package event defines an append-only log event and its wire encoding.
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event is one numbered log record. Payload may be empty.
type Event struct {
	Seq     uint64
	Payload []byte
}

const (
	// SeqLen is the fixed sequence-number prefix.
	SeqLen = 8
	// LenLen is the fixed payload-length prefix.
	LenLen = 4
	// CRCLen is the trailing CRC32 checksum.
	CRCLen = 4
	// HeaderLen is seq + length prefixes.
	HeaderLen = SeqLen + LenLen
	// MaxPayload bounds a single record's payload.
	MaxPayload = 1 << 24 // 16 MiB
)

var (
	// ErrShortRecord means fewer bytes than a record header were present.
	ErrShortRecord = errors.New("event: short record")
	// ErrPayloadTooLarge means the declared length exceeds MaxPayload.
	ErrPayloadTooLarge = errors.New("event: payload too large")
	// ErrCRC means the checksum did not match.
	ErrCRC = errors.New("event: crc mismatch")
)

// EncodedLen returns the on-disk size of e.
func (e Event) EncodedLen() int {
	return HeaderLen + len(e.Payload) + CRCLen
}

// Encode appends the encoded record to dst.
func (e Event) Encode(dst []byte) ([]byte, error) {
	if len(e.Payload) > MaxPayload {
		return nil, ErrPayloadTooLarge
	}
	var buf [HeaderLen]byte
	binary.LittleEndian.PutUint64(buf[:SeqLen], e.Seq)
	binary.LittleEndian.PutUint32(buf[SeqLen:HeaderLen], uint32(len(e.Payload)))
	dst = append(dst, buf[:]...)
	dst = append(dst, e.Payload...)
	var crc [CRCLen]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(
		append(buf[:], e.Payload...)))
	dst = append(dst, crc[:]...)
	return dst, nil
}

// Decode decodes one record from the start of src. It returns the event, the
// number of bytes consumed, and an error.
func Decode(src []byte) (Event, int, error) {
	if len(src) < HeaderLen {
		return Event{}, 0, ErrShortRecord
	}
	seq := binary.LittleEndian.Uint64(src[:SeqLen])
	plen := binary.LittleEndian.Uint32(src[SeqLen:HeaderLen])
	if plen > MaxPayload {
		return Event{}, 0, ErrPayloadTooLarge
	}
	total := HeaderLen + int(plen) + CRCLen
	if len(src) < total {
		return Event{}, 0, ErrShortRecord
	}
	body := src[:HeaderLen+int(plen)]
	want := binary.LittleEndian.Uint32(src[total-CRCLen : total])
	if crc32.ChecksumIEEE(body) != want {
		return Event{}, 0, ErrCRC
	}
	payload := make([]byte, plen)
	copy(payload, src[HeaderLen:HeaderLen+int(plen)])
	return Event{Seq: seq, Payload: payload}, total, nil
}
