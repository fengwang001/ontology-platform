// Package journal implements an append-only change log with a self-describing
// header, length-prefixed records and CRC32 checksums, plus replay.
package journal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

// Magic is the self-describing file header.
const Magic = "IVM1\n"

// Truncation / corruption classifications; use errors.Is to detect them.
var (
	ErrIncompleteHeader = errors.New("journal: incomplete header")
	ErrIncompleteLength = errors.New("journal: incomplete length prefix")
	ErrIncompleteBody   = errors.New("journal: incomplete record body")
	ErrCRCMismatch      = errors.New("journal: CRC mismatch")
	ErrBadMagic         = errors.New("journal: bad magic header")
	ErrOversizeRecord   = errors.New("journal: record larger than declared length")
)

// Writer appends encoded changes to a journal file.
type Writer struct {
	f   *os.File
	w   *bufio.Writer
	sum uint32
}

// Create creates path (truncating it) and writes the header.
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, w: bufio.NewWriter(f)}
	if _, err := w.f.WriteString(Magic); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// OpenForAppend opens an existing journal for appending and validates it.
func OpenForAppend(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, len(Magic))
	if _, err := io.ReadFull(f, buf); err != nil {
		f.Close()
		return nil, classifyHeader(err)
	}
	if string(buf) != Magic {
		f.Close()
		return nil, ErrBadMagic
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, w: bufio.NewWriter(f)}, nil
}

// Append encodes and appends one change followed by its CRC.
func (w *Writer) Append(c change.Change) error {
	payload, err := change.MarshalPayload(c)
	if err != nil {
		return err
	}
	if len(payload) > 0xFFFFFFFF {
		return ErrOversizeRecord
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.w.Write(payload); err != nil {
		return err
	}
	w.sum = crc32.Update(w.sum, crc32.IEEETable, payload)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	_, err = w.w.Write(crc[:])
	return err
}

// Flush flushes the write buffer.
func (w *Writer) Flush() error { return w.w.Flush() }

// Close flushes, fsyncs and closes the journal.
func (w *Writer) Close() error {
	if err := w.w.Flush(); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

func classifyHeader(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrIncompleteHeader
	}
	return err
}

// Replay reads every intact record, invoking fn in arrival order.
//
// It returns the number of applied records and the error that stopped the
// scan. A torn tail record is reported (never silently ignored) and returns the
// records before it intact; a fully intact journal returns a nil error.
func Replay(path string, fn func(change.Change)) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < len(Magic) {
		return 0, ErrIncompleteHeader
	}
	if string(data[:len(Magic)]) != Magic {
		return 0, ErrBadMagic
	}
	data = data[len(Magic):]
	applied := 0
	for len(data) > 0 {
		if len(data) < 4 {
			return applied, ErrIncompleteLength
		}
		n := int(binary.BigEndian.Uint32(data[:4]))
		data = data[4:]
		switch {
		case len(data) < n:
			return applied, ErrIncompleteBody
		case len(data) < n+4:
			return applied, ErrCRCMismatch
		}
		payload := data[:n]
		got := binary.BigEndian.Uint32(data[n : n+4])
		data = data[n+4:]
		if crc32.ChecksumIEEE(payload) != got {
			return applied, ErrCRCMismatch
		}
		c, err := change.UnmarshalPayload(payload)
		if err != nil {
			return applied, err
		}
		fn(c)
		applied++
	}
	return applied, nil
}
