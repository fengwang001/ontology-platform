package hashpart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Segment file layout:
//
//	header: magic "HAG1" (4) | partition uint32 (4) | totalLen uint64 (8)
//	record: payloadLen uint32 | payload | crc32(payload) uint32
//
// totalLen is the full file size including the header; it makes truncation
// at a record boundary detectable.
const (
	HeaderSize = 16
	magic      = "HAG1"
)

// Corruption classes, distinguishable with errors.Is.
var (
	ErrHeaderIncomplete = errors.New("hashpart: header incomplete")
	ErrLengthIncomplete = errors.New("hashpart: length prefix incomplete")
	ErrBodyIncomplete   = errors.New("hashpart: record body incomplete")
	ErrCRCMismatch      = errors.New("hashpart: crc mismatch")
	ErrBadMagic         = errors.New("hashpart: bad magic")
	// ErrInjected is returned when the FailAt write-failure hook fires.
	ErrInjected = errors.New("hashpart: injected write failure")
)

// Writer writes one spill segment atomically: data goes to a .tmp file
// and is renamed to .spill on Close.
type Writer struct {
	f         *os.File
	tmpPath   string
	finalPath string
	part      int
	size      int64
	writes    int
	// FailAt, when > 0, makes the FailAt-th Write call fail with ErrInjected.
	FailAt int
}

// NewWriter creates a segment writer for partition part, sequence seq.
func NewWriter(dir string, part, seq int) (*Writer, error) {
	base := SegmentBase(part, seq)
	tmp := filepath.Join(dir, base+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, tmpPath: tmp, finalPath: filepath.Join(dir, base+".spill"), part: part}
	hdr := make([]byte, HeaderSize)
	copy(hdr, magic)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(part))
	if _, err := w.f.Write(hdr); err != nil {
		_ = w.Abort()
		return nil, err
	}
	w.size = HeaderSize
	return w, nil
}

// Write appends one framed record.
func (w *Writer) Write(payload []byte) error {
	w.writes++
	if w.FailAt > 0 && w.writes == w.FailAt {
		return ErrInjected
	}
	rec := make([]byte, 4+len(payload)+4)
	binary.LittleEndian.PutUint32(rec, uint32(len(payload)))
	copy(rec[4:], payload)
	binary.LittleEndian.PutUint32(rec[4+len(payload):], crc32.ChecksumIEEE(payload))
	n, err := w.f.Write(rec)
	w.size += int64(n)
	return err
}

// Close patches the header, fsyncs and atomically renames to .spill.
func (w *Writer) Close() error {
	hdr := make([]byte, HeaderSize)
	copy(hdr, magic)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(w.part))
	binary.LittleEndian.PutUint64(hdr[8:], uint64(w.size))
	if _, err := w.f.WriteAt(hdr, 0); err != nil {
		_ = w.Abort()
		return err
	}
	if err := w.f.Sync(); err != nil {
		_ = w.Abort()
		return err
	}
	if err := w.f.Close(); err != nil {
		_ = w.Abort()
		return err
	}
	if err := os.Rename(w.tmpPath, w.finalPath); err != nil {
		_ = os.Remove(w.tmpPath)
		return err
	}
	return nil
}

// Abort closes and removes the temporary file.
func (w *Writer) Abort() error {
	_ = w.f.Close()
	return os.Remove(w.tmpPath)
}

// Reader reads one spill segment.
type Reader struct {
	f        *os.File
	part     int
	totalLen int64
	off      int64
}

// Open opens a segment and validates its header.
func Open(path string) (*Reader, error) {
	part := partFromName(filepath.Base(path))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, HeaderSize)
	n, _ := io.ReadFull(f, hdr)
	if n < HeaderSize {
		_ = f.Close()
		return nil, fmt.Errorf("partition %d: offset %d: %w", part, n, ErrHeaderIncomplete)
	}
	if string(hdr[:4]) != magic {
		_ = f.Close()
		return nil, fmt.Errorf("partition %d: offset 0: %w", part, ErrBadMagic)
	}
	return &Reader{
		f:        f,
		part:     int(binary.LittleEndian.Uint32(hdr[4:])),
		totalLen: int64(binary.LittleEndian.Uint64(hdr[8:])),
		off:      HeaderSize,
	}, nil
}

// Partition returns the partition id stored in the header.
func (r *Reader) Partition() int { return r.part }

// Next returns the next record payload, or io.EOF at the clean end.
func (r *Reader) Next() ([]byte, error) {
	if r.off == r.totalLen {
		return nil, io.EOF
	}
	var lenBuf [4]byte
	n, _ := io.ReadFull(r.f, lenBuf[:])
	r.off += int64(n)
	if n < 4 {
		return nil, fmt.Errorf("partition %d: offset %d: %w", r.part, r.off, ErrLengthIncomplete)
	}
	l := int(binary.LittleEndian.Uint32(lenBuf[:]))
	payload := make([]byte, l)
	n, _ = io.ReadFull(r.f, payload)
	r.off += int64(n)
	if n < l {
		return nil, fmt.Errorf("partition %d: offset %d: %w", r.part, r.off, ErrBodyIncomplete)
	}
	var crcBuf [4]byte
	n, _ = io.ReadFull(r.f, crcBuf[:])
	r.off += int64(n)
	if n < 4 || binary.LittleEndian.Uint32(crcBuf[:]) != crc32.ChecksumIEEE(payload) {
		return nil, fmt.Errorf("partition %d: offset %d: %w", r.part, r.off, ErrCRCMismatch)
	}
	return payload, nil
}

// Close closes the underlying file.
func (r *Reader) Close() error { return r.f.Close() }

func partFromName(name string) int {
	if strings.HasPrefix(name, "part-") && len(name) >= 8 {
		if v, err := strconv.Atoi(name[5:8]); err == nil {
			return v
		}
	}
	return -1
}
