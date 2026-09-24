// Package journal appends length-prefixed, CRC32-protected change frames and
// replays them with classifiable corruption errors.
package journal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

// Magic is the self-describing file header.
var Magic = []byte("ONTJ\x01")

// Classifiable replay errors.
var (
	ErrHeaderIncomplete = errors.New("journal: header incomplete")
	ErrLengthIncomplete = errors.New("journal: length prefix incomplete")
	ErrRecordIncomplete = errors.New("journal: record body incomplete")
	ErrCRCMismatch      = errors.New("journal: crc mismatch")
	ErrBadMagic         = errors.New("journal: bad magic")
)

// Writer appends encoded changes to a journal file.
type Writer struct {
	f      *os.File
	closed bool
}

// Create writes a fresh journal with its magic header.
func Create(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(Magic); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Append atomically writes one length-prefixed, CRC-protected frame and fsyncs it.
func (w *Writer) Append(c change.Change) error {
	body, err := change.Encode(c)
	if err != nil {
		return err
	}
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], uint32(len(body)))
	crc := crc32.ChecksumIEEE(body)
	binary.BigEndian.PutUint32(buf[4:8], crc)
	if _, err := w.f.Write(buf[:4]); err != nil {
		return err
	}
	if _, err := w.f.Write(body); err != nil {
		return err
	}
// Write is one syscall so a crash leaves either the full frame or none visible.
	if _, err := w.f.Write(buf[4:8]); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close releases the underlying file.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.f.Close()
}

// Replay reads complete valid frames, invoking fn for each. It stops at the
// first damaged/trailing region and returns its classified error; a clean
// end-of-file returns nil.
func Replay(path string, fn func(change.Change) error) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < len(Magic) {
		return 0, fmt.Errorf("%w: have %d bytes", ErrHeaderIncomplete, len(data))
	}
	for i := range Magic {
		if data[i] != Magic[i] {
			return 0, ErrBadMagic
		}
	}
	pos, n := len(Magic), 0
	for pos < len(data) {
		if len(data)-pos < 4 {
			return n, fmt.Errorf("%w: %d tail bytes", ErrLengthIncomplete, len(data)-pos)
		}
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		end := pos + length
		if end > len(data) {
			missing := end - len(data)
			if missing <= 4 {
				return n, fmt.Errorf("%w: %d of 4 crc bytes", ErrCRCMismatch, 4-missing)
			}
			return n, fmt.Errorf("%w: need %d, have %d", ErrRecordIncomplete, length, len(data)-pos)
		}
		body, trailer := data[pos:end-4], data[end-4:end]
		want := crc32.ChecksumIEEE(body)
		got := binary.BigEndian.Uint32(trailer)
		if want != got {
			return n, ErrCRCMismatch
		}
		c, derr := change.Decode(body)
		if derr != nil {
			return n, fmt.Errorf("journal: decode: %w", derr)
		}
		if err := fn(c); err != nil {
			return n, err
		}
		pos, n = end, n+1
	}
	return n, nil
}

var _ io.Writer // keep io referenced for future streaming use
