// Package journal provides an append-only, length-prefixed, CRC32 protected
// change log in a local file.
//
// File layout:
//
//	[ 16 byte self-describing header ]
//	[ frame ]*
//
// Header:
//
//	magic   "ONTJNL01" (8 bytes)
//	version uint64    (8 bytes, little endian), currently 1
//
// Each frame:
//
//	length uint32 (4 bytes LE) -- byte length of the record body
//	body   change.Encode(...)
//	crc    uint32 (4 bytes LE) -- IEEE CRC32 over length bytes || body
//
// Replay is defensive: every incomplete or corrupt tail position is
// reported as a classifiable error (see errors in errors.go) while all
// preceding intact records are delivered to the caller.
package journal

import (
	"encoding/binary"
	"io"
	"os"

	"ontology/change"
)

// HeaderLen is the fixed size of the self-describing header.
const HeaderLen = 16

var magic = [8]byte{'O', 'N', 'T', 'J', 'N', 'L', '0', '1'}

// FormatVersion is the only on-disk format understood by this package.
const FormatVersion uint64 = 1

// Writer appends change records to a journal file.
type Writer struct {
	f *os.File
}

// Create creates path with a fresh header, failing if it already exists.
func Create(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	h := make([]byte, HeaderLen)
	copy(h[:8], magic[:])
	binary.LittleEndian.PutUint64(h[8:], FormatVersion)
	if _, err := f.Write(h); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Append writes one framed record. The write is followed by Sync so that,
// after Append returns nil, the record survives process crash.
func (w *Writer) Append(c change.Change) error {
	body := change.Encode(c)
	frame := make([]byte, 4+len(body)+4)
	binary.LittleEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	binary.LittleEndian.PutUint32(frame[4+len(body):], crcOf(frame[:4+len(body)]))
	if _, err := w.f.Write(frame); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close releases the underlying file.
func (w *Writer) Close() error { return w.f.Close() }

// Open opens an existing, intact-or-truncated journal for appending. It
// verifies the header and positions the file at its current end so that a
// crash recovery can resume writing after a torn tail has been classified.
func Open(path string) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := make([]byte, HeaderLen)
	if _, err := io.ReadFull(f, h); err != nil {
		f.Close()
		return nil, ErrHeaderIncomplete
	}
	if string(h[:8]) != string(magic[:]) {
		f.Close()
		return nil, ErrBadMagic
	}
	if binary.LittleEndian.Uint64(h[8:]) != FormatVersion {
		f.Close()
		return nil, ErrUnsupportedVersion
	}
	return &Writer{f: f}, nil
}

// ReadAll replays the journal at path. All intact records preceding a
// damaged tail are returned; the first tail problem is returned as a
// classifiable error (nil for a fully intact file). A non-existent file
// yields (nil, nil).
func ReadAll(path string) ([]change.Change, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(data)
}

// Replay opens the journal and feeds intact records to fn in order. It
// returns the number of records delivered and the classified tail error.
func Replay(path string, fn func(change.Change) error) (int, error) {
	recs, err := ReadAll(path)
	for _, c := range recs {
		if ferr := fn(c); ferr != nil {
			return 0, ferr
		}
	}
	return len(recs), err
}
