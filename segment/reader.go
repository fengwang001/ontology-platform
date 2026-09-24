package segment

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"

	"ontology/event"
)

// Reader sequentially reads a segment while counting bytes touched.
type Reader struct {
	f     *os.File
	h     Header
	size  int64
	off   int64
	read  int64
	recNo uint64
}

// Open opens a segment for reading and validates its header.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	var hb [HeaderSize]byte
	if fi.Size() < HeaderSize {
		f.Close()
		return nil, ErrHeaderTruncated
	}
	if _, err := io.ReadFull(f, hb[:]); err != nil {
		f.Close()
		return nil, ErrHeaderTruncated
	}
	h, err := DecodeHeader(hb[:])
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, h: h, size: fi.Size(), off: HeaderSize, read: HeaderSize}, nil
}

// Header returns the segment header.
func (r *Reader) Header() Header { return r.h }

// BytesRead returns file bytes read since open.
func (r *Reader) BytesRead() int64 { return r.read }

// Close releases the file.
func (r *Reader) Close() error { return r.f.Close() }

// Next reads the next frame at absolute frame offset curOff.
// It returns the event, its frame offset and offset past the frame.
// At a clean end it returns io.EOF with the current offset.
func (r *Reader) Next() (event.Event, int64, int64, error) {
	if r.off >= r.size {
		if r.recNo < r.h.Count {
			return event.Event{}, r.off, r.off, ErrLengthTruncated
		}
		return event.Event{}, r.off, r.off, io.EOF
	}
	var lb [event.LenSize]byte
	if err := readAtFull(r.f, lb[:], r.off, ErrLengthTruncated); err != nil {
		return event.Event{}, r.off, r.off, err
	}
	r.read += event.LenSize
	n := int64(binary.BigEndian.Uint32(lb[:]))
	rest := make([]byte, n+event.CRCSize)
	if err := readAtFull(r.f, rest, r.off+event.LenSize, ErrBodyTruncated); err != nil {
		return event.Event{}, r.off, r.off, err
	}
	r.read += n + event.CRCSize
	payload := rest[:n]
	got := binary.BigEndian.Uint32(rest[n:])
	if got != crc32.ChecksumIEEE(payload) {
		return event.Event{}, r.off, r.off, ErrCRCMismatch
	}
	frameOff := r.off
	r.off += event.FrameOver + n
	ev := event.Event{Seq: r.h.Base + r.recNo, Payload: payload}
	r.recNo++
	return ev, frameOff, r.off, nil
}

// SeekTo repositions the sequential cursor at an absolute frame offset.
func (r *Reader) SeekTo(off int64) {
	r.off = off
}

// SetRecNo sets the ordinal of the next frame (used after a seek).
func (r *Reader) SetRecNo(n uint64) { r.recNo = n }

// RecNo returns frames decoded so far since the last seek/open.
func (r *Reader) RecNo() uint64 { return r.recNo }
