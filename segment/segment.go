// Package segment implements append-only segment files: a self-describing
// header followed by length-prefixed, CRC-protected event records.
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ontology/event"
)

const (
	// HeaderSize is the fixed byte size of the segment header.
	HeaderSize = 24
	magic      = "SEG1"
	version    = 1
)

var (
	ErrShortHeader = errors.New("segment: header incomplete")
	ErrBadMagic    = errors.New("segment: bad magic or version")
)

// Header describes a segment: first sequence number and event count.
type Header struct {
	FirstSeq uint64
	Count    uint64
}

// End returns the first sequence number after this segment (exclusive).
func (h Header) End() uint64 { return h.FirstSeq + h.Count }

func encodeHeader(h Header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf, magic)
	binary.BigEndian.PutUint32(buf[4:], version)
	binary.BigEndian.PutUint64(buf[8:], h.FirstSeq)
	binary.BigEndian.PutUint64(buf[16:], h.Count)
	return buf
}

func decodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderSize {
		return Header{}, ErrShortHeader
	}
	if string(buf[:4]) != magic || binary.BigEndian.Uint32(buf[4:]) != version {
		return Header{}, ErrBadMagic
	}
	return Header{
		FirstSeq: binary.BigEndian.Uint64(buf[8:]),
		Count:    binary.BigEndian.Uint64(buf[16:]),
	}, nil
}

// Name returns the canonical file name for a segment starting at firstSeq.
func Name(firstSeq uint64) string { return fmt.Sprintf("seg-%016d.log", firstSeq) }

// List returns the segment file paths in dir, sorted by first sequence.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "seg-") && strings.HasSuffix(e.Name(), ".log") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Writer appends events to a single segment file.
type Writer struct {
	f   *os.File
	hdr Header
}

// Create starts a new segment in dir whose first event has sequence firstSeq.
func Create(dir string, firstSeq uint64) (*Writer, error) {
	path := filepath.Join(dir, Name(firstSeq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, hdr: Header{FirstSeq: firstSeq}}
	if _, err := f.Write(encodeHeader(w.hdr)); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// Append writes one event record, then updates the header count. The record
// is written with a single Write so concurrent readers never observe a
// partial record below the published count.
func (w *Writer) Append(payload []byte) error {
	if _, err := w.f.Write(event.Encode(payload)); err != nil {
		return err
	}
	w.hdr.Count++
	_, err := w.f.WriteAt(encodeHeader(w.hdr), 0)
	return err
}

// Count returns the number of events appended so far.
func (w *Writer) Count() uint64 { return w.hdr.Count }

// Close flushes and closes the segment file.
func (w *Writer) Close() error { return w.f.Close() }

// Reader provides sequential reads over an existing segment file.
type Reader struct {
	f    *os.File
	Hdr  Header
	size int64
}

// Open opens a segment file and validates its header.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() < HeaderSize {
		f.Close()
		return nil, ErrShortHeader
	}
	buf := make([]byte, HeaderSize)
	if _, err := f.ReadAt(buf, 0); err != nil {
		f.Close()
		return nil, err
	}
	hdr, err := decodeHeader(buf)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, Hdr: hdr, size: st.Size()}, nil
}

// Size returns the file size in bytes.
func (r *Reader) Size() int64 { return r.size }

// Section returns a reader over the file region [offset, EOF).
func (r *Reader) Section(offset uint64) io.Reader {
	return io.NewSectionReader(r.f, int64(offset), r.size-int64(offset))
}

// Close closes the underlying file.
func (r *Reader) Close() error { return r.f.Close() }

// RewriteCount opens the segment at path and stores a corrected event count
// in its header, preserving the first sequence number.
func RewriteCount(path string, count uint64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, HeaderSize)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return err
	}
	hdr, err := decodeHeader(buf)
	if err != nil {
		return err
	}
	hdr.Count = count
	_, err = f.WriteAt(encodeHeader(hdr), 0)
	return err
}
