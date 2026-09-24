// Package segment implements append writing and sequential reading of one
// self-describing segment file: fixed header followed by length-prefixed,
// CRC32-protected event frames.
package segment

import (
	"encoding/binary"
	"errors"
	"io"
	"os"

	"ontology/event"
)

const (
	Magic      = "ONTOSEG1"
	HeaderSize = 32
)

var (
	ErrBadMagic    = errors.New("segment: bad header magic")
	ErrShortHeader = errors.New("segment: incomplete header")
)

// Header describes a segment.
type Header struct {
	FirstSeq uint64 // sequence number of the first frame
	Count    uint32 // number of frames recorded in the header
}

// Writer appends frames and keeps the header count in sync after each frame.
type Writer struct {
	f      *os.File
	h      Header
	synced bool
}

// Create creates path with a fresh header and returns its writer.
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, h: Header{FirstSeq: firstSeq}}
	if err := w.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(HeaderSize, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// OpenWriter opens an existing segment for appending.
func OpenWriter(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h, err := readHeaderAt(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, h: h, synced: true}, nil
}

func (w *Writer) writeHeader() error {
	buf := make([]byte, HeaderSize)
	copy(buf[:8], Magic)
	binary.BigEndian.PutUint64(buf[8:16], w.h.FirstSeq)
	binary.BigEndian.PutUint32(buf[16:20], w.h.Count)
	if _, err := w.f.WriteAt(buf, 0); err != nil {
		return err
	}
	return nil
}

// Append writes one frame and refreshes the header count before returning.
func (w *Writer) Append(e event.Event) error {
	if _, err := w.f.Write(e.Encode(nil)); err != nil {
		return err
	}
	w.h.Count++
	if err := w.writeHeader(); err != nil {
		return err
	}
	return nil
}

// Header returns the current header.
func (w *Writer) Header() Header { return w.h }

// SeekOffset returns the current frame-region offset (relative to header).
func (w *Writer) SeekOffset() (int64, error) {
	st, err := w.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size() - HeaderSize, nil
}

// Sync flushes buffered writes to stable storage.
func (w *Writer) Sync() error { return w.f.Sync() }

// Close flushes and closes the segment.
func (w *Writer) Close() error {
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

func readHeaderAt(f *os.File) (Header, error) {
	buf := make([]byte, HeaderSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Header{}, err
	}
	if n < HeaderSize {
		return Header{}, ErrShortHeader
	}
	if string(buf[:8]) != Magic {
		return Header{}, ErrBadMagic
	}
	return Header{
		FirstSeq: binary.BigEndian.Uint64(buf[8:16]),
		Count:    binary.BigEndian.Uint32(buf[16:20]),
	}, nil
}

// Reader sequentially reads frames starting right after the header.
type Reader struct {
	fr     *FrameReader
	Header Header
}

// OpenReader opens a segment for sequential reads.
func OpenReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	h, err := readHeaderAt(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{fr: NewFrameReader(f, HeaderSize), Header: h}, nil
}

// Next reads the next frame; io.EOF marks a clean end.
func (r *Reader) Next() (event.Event, error) { return r.fr.Next() }

// Offset returns frame-region bytes consumed so far.
func (r *Reader) Offset() int64 { return r.fr.BytesRead() }
