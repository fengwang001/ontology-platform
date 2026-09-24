// Package segment implements append-write and sequential-read of one
// self-describing segment file: a fixed header followed by length-prefixed,
// CRC32-protected event frames.
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"hash/crc32"

	"ontology/event"
)

func crc32sum(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

const (
	magicV1    = 0x53454731 // "SEG1"
	versionV1  = uint16(1)
	// HeaderSize is the fixed on-disk segment header size.
	HeaderSize = 24
	lenSize    = 4
	crcSize    = 4
)

var (
	// ErrTruncHead: the fixed header could not be fully read.
	ErrTruncHead = errors.New("segment: incomplete header")
	// ErrTruncLen: a frame length prefix could not be fully read.
	ErrTruncLen = errors.New("segment: incomplete length prefix")
	// ErrTruncBody: a frame body could not be fully read.
	ErrTruncBody = errors.New("segment: incomplete event body")
	// ErrCRCMismatch: CRC field missing or does not match the body.
	ErrCRCMismatch = errors.New("segment: crc mismatch")
	errBadMagic    = errors.New("segment: bad magic/version")
)

// Header is the self-describing segment header.
type Header struct {
	FirstSeq int64
	Count    int64
}

// Writer appends events to one segment file. It is safe for concurrent use.
type Writer struct {
	mu   sync.Mutex
	f    *os.File
	h    Header
	off  int64
}

// Create creates a new segment file at path with the given first sequence.
func Create(path string, firstSeq int64) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, h: Header{FirstSeq: firstSeq}, off: HeaderSize}
	if err := w.writeHeader(); err != nil {
		f.Close()
	return nil, err
	}
	return w, nil
}

func encodeHeader(h Header) []byte {
	b := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(b[0:4], magicV1)
	binary.LittleEndian.PutUint16(b[4:6], versionV1)
	binary.LittleEndian.PutUint64(b[8:16], uint64(h.FirstSeq))
	binary.LittleEndian.PutUint64(b[16:24], uint64(h.Count))
	return b
}

func (w *Writer) writeHeader() error {
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := w.f.Write(encodeHeader(w.h))
	return err
}

// FirstSeq reports the segment's first sequence number.
func (w *Writer) FirstSeq() int64 { return w.h.FirstSeq }

// Append writes one event as a single frame and updates the header count.
// It returns the byte offset of the frame start.
func (w *Writer) Append(e event.Event) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	body := e.Encode(nil)
	frame := make([]byte, 0, lenSize+len(body)+crcSize)
	var l [lenSize]byte
	binary.LittleEndian.PutUint32(l[:], uint32(len(body)))
	frame = append(frame, l[:]...)
	frame = append(frame, body...)
	var c [crcSize]byte
	binary.LittleEndian.PutUint32(c[:], crc32sum(body))
	frame = append(frame, c[:]...)
	if _, err := w.f.Seek(w.off, io.SeekStart); err != nil {
		return 0, err
	}
	if _, err := w.f.Write(frame); err != nil {
		return 0, err
	}
	off := w.off
	w.off += int64(len(frame))
	w.h.Count++
	if err := w.writeHeader(); err != nil {
		return 0, err
	}
	return off, nil
}

// Sync flushes writes to stable storage.
func (w *Writer) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

// Close flushes and closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}
