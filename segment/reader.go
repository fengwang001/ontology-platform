package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"ontology/event"
)

// Frame is one decoded event plus its file byte offset and encoded size.
type Frame struct {
	Event    event.Event
	Offset   int64
	BodySize int // event encoding bytes (excludes len and crc)
}

// Reader sequentially reads one segment through an independent file handle.
type Reader struct {
	f   *os.File
	h   Header
	off int64
}

// Open opens a segment for reading and validates its header.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, off: HeaderSize}
	h, err := r.readHeader()
	if err != nil {
		f.Close()
		return nil, err
	}
	r.h = h
	return r, nil
}

func (r *Reader) readHeader() (Header, error) {
	b := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r.f, b); err != nil {
		return Header{}, fmt.Errorf("%w: %v", ErrTruncHead, err)
	}
	if binary.LittleEndian.Uint32(b[0:4]) != magicV1 ||
		binary.LittleEndian.Uint16(b[4:6]) != versionV1 {
		return Header{}, errBadMagic
	}
	return Header{
		FirstSeq: int64(binary.LittleEndian.Uint64(b[8:16])),
		Count:    int64(binary.LittleEndian.Uint64(b[16:24])),
	}, nil
}

// Header returns the parsed segment header.
func (r *Reader) Header() Header { return r.h }

// Close releases the file handle.
func (r *Reader) Close() error { return r.f.Close() }

// SeekTo seeks to a frame offset to start scanning.
func (r *Reader) SeekTo(off int64) error {
	if _, err := r.f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	r.off = off
	return nil
}

func readFullAt(f *os.File, buf []byte, off int64) (int, error) {
	n, err := f.ReadAt(buf, off)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

// ReadFrameAt strictly classifies one frame at off. It returns ErrTruncLen,
// ErrTruncBody, or ErrCRCMismatch for an unreadable/damaged frame.
func ReadFrameAt(f *os.File, off int64) (Frame, int64, error) {
	var lb [lenSize]byte
	if _, err := readFullAt(f, lb[:], off); err != nil {
		return Frame{}, off, fmt.Errorf("%w at %d", ErrTruncLen, off)
	}
	n := int64(binary.LittleEndian.Uint32(lb[:]))
	if n < event.HeaderSize || n > 1<<30 {
		return Frame{}, off, fmt.Errorf("%w: illegal len %d", ErrCRCMismatch, n)
	}
	body := make([]byte, n)
	if _, err := readFullAt(f, body, off+lenSize); err != nil {
		return Frame{}, off, fmt.Errorf("%w at %d", ErrTruncBody, off)
	}
	var cb [crcSize]byte
	if _, err := readFullAt(f, cb[:], off+lenSize+n); err != nil {
		return Frame{}, off, fmt.Errorf("%w at %d", ErrCRCMismatch, off)
	}
	if binary.LittleEndian.Uint32(cb[:]) != crc32sum(body) {
		return Frame{}, off, fmt.Errorf("%w at %d", ErrCRCMismatch, off)
	}
	e, err := event.Decode(body)
	if err != nil {
		return Frame{}, off, fmt.Errorf("%w: %v", ErrCRCMismatch, err)
	}
	end := off + lenSize + n + crcSize
	return Frame{Event: e, Offset: off, BodySize: int(n)}, end, nil
}

// Next returns the next frame in sequence. At the clean end of a prefix it
// returns io.EOF; a damaged/torn frame ends the prefix (the previous frames
// remain valid). The boolean is false at the clean prefix boundary.
func (r *Reader) Next() (Frame, bool, error) {
	var lb [lenSize]byte
	n, err := readFullAt(r.f, lb[:], r.off)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		if n == 0 {
			return Frame{}, false, io.EOF
		}
		return Frame{}, false, nil
	}
	if err != nil {
		return Frame{}, false, err
	}
	fr, end, ferr := ReadFrameAt(r.f, r.off)
	if ferr != nil {
		return Frame{}, false, nil // torn/partial frame: stop at clean prefix
	}
	r.off = end
	return fr, true, nil
}
