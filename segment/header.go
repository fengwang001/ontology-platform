// Package segment stores one contiguous, append-only slice of the event log.
// File format: 32-byte header followed by length-prefixed CRC frames.
package segment

import (
	"encoding/binary"
	"errors"
	"io"
)

// HeaderSize is the fixed on-disk segment header length in bytes.
const HeaderSize = 32

var headerMagic = [8]byte{'O', 'N', 'L', 'O', 'G', 'S', 'E', '1'}

var (
	// ErrBadHeader means the header magic is wrong.
	ErrBadHeader = errors.New("segment: bad header")
	// ErrHeaderTruncated means fewer than HeaderSize bytes are present.
	ErrHeaderTruncated = errors.New("segment: header truncated")
	// ErrLengthTruncated means a frame's 4-byte length prefix is partial/absent.
	ErrLengthTruncated = errors.New("segment: length prefix truncated")
	// ErrBodyTruncated means a declared body or its CRC is not fully present.
	ErrBodyTruncated = errors.New("segment: event body truncated")
	// ErrCRCMismatch means a complete frame failed its CRC check.
	ErrCRCMismatch = errors.New("segment: crc mismatch")
	// ErrSeqGap means two adjacent segments are not sequence-contiguous.
	ErrSeqGap = errors.New("segment: sequence gap between segments")
)

// Header describes a segment: Base is its first event sequence number and
// Count is the number of readable events.
type Header struct {
	Base  uint64
	Count uint64
}

// Encode writes the fixed 32-byte header.
func (h Header) Encode() []byte {
	b := make([]byte, HeaderSize)
	copy(b, headerMagic[:])
	binary.BigEndian.PutUint64(b[8:], h.Base)
	binary.BigEndian.PutUint64(b[16:], h.Count)
	return b
}

// DecodeHeader parses exactly HeaderSize bytes.
func DecodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, ErrHeaderTruncated
	}
	var magic [8]byte
	copy(magic[:], b)
	if magic != headerMagic {
		return Header{}, ErrBadHeader
	}
	return Header{
		Base:  binary.BigEndian.Uint64(b[8:]),
		Count: binary.BigEndian.Uint64(b[16:]),
	}, nil
}

// readAtFull reads exactly len(p) bytes at off, classifying short reads.
func readAtFull(f io.ReaderAt, p []byte, off int64, short error) error {
	n, err := f.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF || err != nil {
		return short
	}
	return nil
}
