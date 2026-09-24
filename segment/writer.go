package segment

import (
	"errors"
	"io"
	"os"
	"sync"

	"ontology/event"
)

// Writer is an append-only segment. The full frame is submitted in one Write,
// so concurrent readers (holding the log lock) only ever see whole frames.
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	h      Header
	size   int64
	closed bool
}

// Create creates a new segment at path starting at sequence base.
func Create(path string, base uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	h := Header{Base: base, Count: 0}
	if _, err := f.Write(h.Encode()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, h: h, size: HeaderSize}, nil
}

// OpenAppend opens an existing segment for appending, trusting its header.
func OpenAppend(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	var hb [HeaderSize]byte
	if _, err := io.ReadFull(f, hb[:]); err != nil {
		f.Close()
		return nil, ErrBadHeader
	}
	h, err := DecodeHeader(hb[:])
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, h: h, size: fi.Size()}, nil
}

// Append appends one event. Its sequence must equal the next expected one.
func (w *Writer) Append(e event.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("segment: writer closed")
	}
	if want := w.h.Base + w.h.Count; e.Seq != want {
		return errors.New("segment: out-of-order append")
	}
	buf := e.Encode(nil)
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	w.size += int64(len(buf))
	w.h.Count++
	if _, err := w.f.WriteAt(w.h.Encode(), 0); err != nil {
		return err
	}
	return nil
}

// Base returns the segment's first sequence number.
func (w *Writer) Base() uint64 { return w.h.Base }

// Count returns the number of appended events.
func (w *Writer) Count() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.h.Count
}

// Close rewrites the header count, syncs and closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if _, err := w.f.WriteAt(w.h.Encode(), 0); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}
