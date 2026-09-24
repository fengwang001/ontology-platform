// Package journal implements the append-only change log: a
// self-describing header followed by length-prefixed, CRC32-protected
// records. Replay decodes the complete-record prefix and classifies any
// truncation it meets.
package journal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/change"
)

// HeaderLen is the size of the self-describing file header:
// magic(4) + format version(2) + header length(2).
const HeaderLen = 8

const maxRecord = 1 << 20

var magic = [4]byte{'O', 'V', 'J', '1'}

// Class is the truncation classification of a replayed log.
type Class int

const (
	ClassOK Class = iota
	ClassHeaderIncomplete
	ClassLengthIncomplete
	ClassBodyIncomplete
	ClassCRCMismatch
)

func (c Class) String() string {
	switch c {
	case ClassOK:
		return "ok"
	case ClassHeaderIncomplete:
		return "header-incomplete"
	case ClassLengthIncomplete:
		return "length-prefix-incomplete"
	case ClassBodyIncomplete:
		return "record-body-incomplete"
	case ClassCRCMismatch:
		return "crc-mismatch"
	}
	return "unknown"
}

var (
	ErrHeaderIncomplete = errors.New("journal: header incomplete")
	ErrLengthIncomplete = errors.New("journal: length prefix incomplete")
	ErrBodyIncomplete   = errors.New("journal: record body incomplete")
	ErrCRCMismatch      = errors.New("journal: crc mismatch")
)

// Journal appends change records to one log file.
type Journal struct {
	f    *os.File
	path string
}

// Create starts a fresh log at path, writing the header.
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, HeaderLen)
	copy(hdr, magic[:])
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	binary.LittleEndian.PutUint16(hdr[6:8], HeaderLen)
	if _, err := f.Write(hdr); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f, path: path}, nil
}

// Path returns the log file path.
func (j *Journal) Path() string { return j.path }

// Open opens an existing log for appending after validating its header.
func Open(path string) (*Journal, error) {
	if _, cls, err := Replay(path); cls == ClassHeaderIncomplete {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Journal{f: f, path: path}, nil
}

// Append encodes c and writes len|body|crc, then flushes to the OS so a
// crash after Append never loses the record.
func (j *Journal) Append(c change.Change) error {
	body, err := change.Encode(c)
	if err != nil {
		return err
	}
	rec := make([]byte, 4+len(body)+4)
	binary.LittleEndian.PutUint32(rec[:4], uint32(len(body)))
	copy(rec[4:], body)
	binary.LittleEndian.PutUint32(rec[4+len(body):], crc32.ChecksumIEEE(body))
	if _, err := j.f.Write(rec); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close closes the underlying file.
func (j *Journal) Close() error { return j.f.Close() }

// Replay decodes the complete-record prefix of the log at path. It
// returns the decoded records, the truncation class, and a decidable
// error (nil when class is ClassOK).
func Replay(path string) ([]change.Change, Class, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, ClassHeaderIncomplete, err
	}
	return Parse(buf)
}

// Parse is Replay operating on an in-memory image of a log file.
func Parse(buf []byte) ([]change.Change, Class, error) {
	if len(buf) < HeaderLen {
		return nil, ClassHeaderIncomplete, ErrHeaderIncomplete
	}
	if string(buf[:4]) != string(magic[:]) ||
		binary.LittleEndian.Uint16(buf[6:8]) != HeaderLen {
		return nil, ClassHeaderIncomplete, fmt.Errorf("%w: bad magic or header length", ErrHeaderIncomplete)
	}
	var out []change.Change
	off := HeaderLen
	for off < len(buf) {
		rest := len(buf) - off
		if rest < 4 {
			return out, ClassLengthIncomplete, ErrLengthIncomplete
		}
		n := int(binary.LittleEndian.Uint32(buf[off : off+4]))
		if n <= 0 || n > maxRecord {
			return out, ClassCRCMismatch, fmt.Errorf("%w: implausible length %d", ErrCRCMismatch, n)
		}
		if rest-4 < n {
			return out, ClassBodyIncomplete, ErrBodyIncomplete
		}
		body := buf[off+4 : off+4+n]
		if rest-4-n < 4 {
			return out, ClassCRCMismatch, ErrCRCMismatch
		}
		want := binary.LittleEndian.Uint32(buf[off+4+n : off+8+n])
		if crc32.ChecksumIEEE(body) != want {
			return out, ClassCRCMismatch, ErrCRCMismatch
		}
		c, err := change.Decode(body)
		if err != nil {
			return out, ClassCRCMismatch, fmt.Errorf("%w: %v", ErrCRCMismatch, err)
		}
		out = append(out, c)
		off += 4 + n + 4
	}
	return out, ClassOK, nil
}
