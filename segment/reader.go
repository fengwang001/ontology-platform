package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"ontology/event"
)

// Reader sequentially scans one segment from a byte offset. The file size is
// snapshotted at Open, so concurrent appends never expose a torn record.
type Reader struct {
	f     *os.File
	hdr   Header
	size  int64
	pos   int64
	start int64
}

// Open opens path and snapshots its length.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hdr, err := ReadHeader(path)
	if err != nil {
		f.Close()
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, hdr: hdr, size: fi.Size(), pos: hdrLen, start: hdrLen}, nil
}

// Header returns the segment header.
func (r *Reader) Header() Header { return r.hdr }

// SnapshotSize returns the file length captured at Open.
func (r *Reader) SnapshotSize() int64 { return r.size }

// SeekTo starts the next scan at byte offset off (relative to file start).
func (r *Reader) SeekTo(off int64) {
	if off < hdrLen {
		off = hdrLen
	}
	r.pos = off
	r.start = off
}

// BytesRead reports record bytes consumed since the last SeekTo.
func (r *Reader) BytesRead() int64 { return r.pos - r.start }

// Next reads the next event. io.EOF marks a clean end; otherwise the error is
// one of the classified corruption sentinels.
func (r *Reader) Next() (event.Event, error) {
	if r.pos >= r.size {
		return event.Event{}, io.EOF
	}
	remaining := r.size - r.pos
	if remaining < int64(event.HeaderLen) {
		return event.Event{}, fmt.Errorf("%w at %d: %d bytes left",
			ErrLengthPrefixIncomplete, r.pos, remaining)
	}
	var head [event.HeaderLen]byte
	if _, err := r.f.ReadAt(head[:], r.pos); err != nil {
		return event.Event{}, err
	}
	plen := binary.LittleEndian.Uint32(head[event.SeqLen:])
	if plen > event.MaxPayload || r.pos+int64(event.HeaderLen)+int64(plen) > maxBytes {
		return event.Event{}, ErrBadMagic
	}
	bodyEnd := r.pos + int64(event.HeaderLen) + int64(plen)
	if remaining < int64(event.HeaderLen)+int64(plen) {
		return event.Event{}, fmt.Errorf("%w at %d: %d bytes left",
			ErrBodyIncomplete, r.pos, remaining)
	}
	if remaining < int64(event.HeaderLen)+int64(plen)+int64(event.CRCLen) {
		return event.Event{}, fmt.Errorf("%w at %d: crc cut, %d bytes left",
			ErrCRCMismatch, r.pos, remaining)
	}
	buf := make([]byte, event.HeaderLen+plen+event.CRCLen)
	if _, err := r.f.ReadAt(buf, r.pos); err != nil {
		return event.Event{}, err
	}
	ev, n, err := event.Decode(buf)
	if err != nil {
		if errors.Is(err, event.ErrCRC) {
			return event.Event{}, fmt.Errorf("%w at %d", ErrCRCMismatch, r.pos)
		}
		return event.Event{}, err
	}
	r.pos += int64(n)
	return ev, nil
}

// Close releases the file.
func (r *Reader) Close() error { return r.f.Close() }
