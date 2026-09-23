// Package journal implements an append-only, CRC-protected change log with a
// self-describing header. Replay classifies any torn tail into one of four
// decidable errors and applies only the intact record prefix.
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

const (
	magic      = "IVWJ"
	formatVer  = byte(1)
	headerSize = 6 // magic(4) + version(1) + reserved(1)
	lenSize    = 4
	crcSize    = 4
)

var (
	// ErrShortHeader: fewer than headerSize bytes were present.
	ErrShortHeader = errors.New("journal: incomplete header")
	// ErrBadHeader: the header bytes did not match magic/version.
	ErrBadHeader = errors.New("journal: bad header")
	// ErrShortLength: the 4-byte record length prefix was cut.
	ErrShortLength = errors.New("journal: incomplete length prefix")
	// ErrShortBody: the declared payload body was cut.
	ErrShortBody = errors.New("journal: incomplete record body")
	// ErrCRC: the checksum was missing/cut or did not match.
	ErrCRC = errors.New("journal: crc mismatch")

	ieee = crc32.IEEETable
)

// Writer appends framed changes to one file and fsyncs each append.
type Writer struct {
	f *os.File
	w *bufio.Writer
}

// Create creates a new journal file and writes its header.
func Create(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(header()); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, w: bufio.NewWriter(f)}, nil
}

// Append encodes, frames and durably appends one change.
func (w *Writer) Append(c change.Change) error {
	body := change.EncodePayload(c)
	frame := make([]byte, 0, lenSize+len(body)+crcSize)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(body)))
	frame = append(frame, body...)
	frame = binary.BigEndian.AppendUint32(frame, crc32.Checksum(body, ieee))
	if _, err := w.w.Write(frame); err != nil {
		return err
	}
	if err := w.w.Flush(); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close flushes and closes the file.
func (w *Writer) Close() error {
	if err := w.w.Flush(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// Replay opens a journal and invokes fn for each intact record in order. It
// returns the number of records successfully delivered. A torn tail yields a
// classified error; records before it remain applied.
func Replay(path string, fn func(change.Change) error) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
	return 0, err
	}
	return ReplayBytes(data, fn)
}

// ReplayBytes is Replay over an in-memory image (used by truncation tests).
func ReplayBytes(data []byte, fn func(change.Change) error) (int, error) {
	if len(data) < headerSize {
		return 0, ErrShortHeader
	}
	if string(data[:4]) != magic || data[4] != formatVer || data[5] != 0 {
		return 0, ErrBadHeader
	}
	pos := headerSize
	applied := 0
	for pos < len(data) {
		tail := data[pos:]
		if len(tail) < lenSize {
			return applied, ErrShortLength
		}
		n := int(binary.BigEndian.Uint32(tail[:lenSize]))
		if len(tail) < lenSize+n {
			return applied, ErrShortBody
		}
		body := tail[lenSize : lenSize+n]
		if len(tail) < lenSize+n+crcSize {
			return applied, ErrCRC
		}
		want := binary.BigEndian.Uint32(tail[lenSize+n : lenSize+n+crcSize])
		if crc32.Checksum(body, ieee) != want {
			return applied, ErrCRC
		}
		c, err := change.DecodePayload(body)
		if err != nil {
			return applied, err
		}
		if err := fn(c); err != nil {
			return applied, err
		}
		applied++
		pos += lenSize + n + crcSize
}
	if pos != len(data) {
		return applied, io.ErrUnexpectedEOF
	}
	return applied, nil
}

func header() []byte {
	h := make([]byte, headerSize)
	copy(h[:4], magic)
	h[4] = formatVer
	return h
}
