// Package segment implements append/scan access to one self-describing log
// segment file: fixed header, length-prefixed CRC32-checked event records.
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"ontology/event"
)

const (
	magic    = "OSEG"
	hdrLen   = 24 // magic(4) + firstSeq(8) + count(8)
	maxBytes = 1 << 30
)

var (
	// ErrBadMagic means the header magic is wrong.
	ErrBadMagic = errors.New("segment: bad magic")
	// ErrHeaderIncomplete means the file ends inside the 24-byte header.
	ErrHeaderIncomplete = errors.New("segment: header incomplete")
	// ErrLengthPrefixIncomplete means a record length prefix is cut.
	ErrLengthPrefixIncomplete = errors.New("segment: length prefix incomplete")
	// ErrBodyIncomplete means declared payload/crc runs past EOF.
	ErrBodyIncomplete = errors.New("segment: event body incomplete")
	// ErrCRCMismatch means a fully present record fails its checksum.
	ErrCRCMismatch = event.ErrCRC
)

// Writer appends events to one segment file.
type Writer struct {
	f        *os.File
	firstSeq uint64
	count    uint64
}

// Create creates path with firstSeq recorded in its header.
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	var hdr [hdrLen]byte
	copy(hdr[:4], magic)
	binary.LittleEndian.PutUint64(hdr[4:12], firstSeq)
	if _, err := f.Write(hdr[:]); err != nil {
		f.Close()
	return nil, err
	}
	return &Writer{f: f, firstSeq: firstSeq}, nil
}

// FirstSeq reports the header's first sequence number.
func (w *Writer) FirstSeq() uint64 { return w.firstSeq }

// Count reports how many events have been appended.
func (w *Writer) Count() uint64 { return w.count }

// Append writes one event and refreshes the header count.
func (w *Writer) Append(e event.Event) (offset int64, err error) {
	if e.Seq != w.firstSeq+w.count {
		return 0, fmt.Errorf("segment: seq %d, want %d", e.Seq, w.firstSeq+w.count)
	}
	if off, err := w.f.Seek(0, io.SeekEnd); err == nil {
		offset = off
	}
	raw, err := e.Encode(nil)
	if err != nil {
		return 0, err
	}
	if _, err := w.f.Write(raw); err != nil {
		return 0, err
	}
	w.count++
	var nb [8]byte
	binary.LittleEndian.PutUint64(nb[:], w.count)
	if _, err := w.f.WriteAt(nb[:], 16); err != nil {
		return 0, err
	}
	return offset, w.f.Sync()
}

// Close flushes and closes the file.
func (w *Writer) Close() error { return w.f.Close() }

// Header describes a segment.
type Header struct {
	FirstSeq uint64
	Count    uint64
}

// ReadHeader reads only the 24-byte header of path.
func ReadHeader(path string) (Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer f.Close()
	var hdr [hdrLen]byte
	n, err := io.ReadFull(f, hdr[:])
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return Header{}, fmt.Errorf("%w: %d bytes", ErrHeaderIncomplete, n)
	}
	if err != nil {
		return Header{}, err
	}
	if string(hdr[:4]) != magic {
		return Header{}, ErrBadMagic
	}
	return Header{
		FirstSeq: binary.LittleEndian.Uint64(hdr[4:12]),
		Count:    binary.LittleEndian.Uint64(hdr[12:20]),
	}, nil
}
