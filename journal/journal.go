// Package journal appends changes to a local log and replays it.
// Layout: header(magic 4B + format version 4B), then per record
// length(4B) + payload + CRC32(payload).
package journal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/change"
)

// HeaderSize is the byte length of the self-describing header.
const HeaderSize = 8

var magic = [4]byte{'O', 'J', '1', 0}

// Class categorizes where replay of a truncated/corrupt log stopped.
type Class int

const (
	ClassNone Class = iota
	ClassHeaderIncomplete
	ClassLengthIncomplete
	ClassRecordIncomplete
	ClassCRCMismatch
)

func (c Class) String() string {
	switch c {
	case ClassHeaderIncomplete:
		return "header incomplete"
	case ClassLengthIncomplete:
		return "length prefix incomplete"
	case ClassRecordIncomplete:
		return "record body incomplete"
	case ClassCRCMismatch:
		return "crc mismatch"
	}
	return "none"
}

// ReplayError reports the classification and offset of a replay failure.
type ReplayError struct {
	Class  Class
	Offset int
}

func (e *ReplayError) Error() string {
	return fmt.Sprintf("journal: replay stopped at offset %d: %s", e.Offset, e.Class)
}

// Journal is an append-only change log.
type Journal struct{ f *os.File }

// Create starts a fresh log at path, writing the header.
func Create(path string) (*Journal, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	h := make([]byte, HeaderSize)
	copy(h, magic[:])
	binary.BigEndian.PutUint32(h[4:], 1)
	if _, err := f.Write(h); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Append writes one length-prefixed, CRC-protected record.
func (j *Journal) Append(c change.Change) error {
	p := c.Encode()
	rec := make([]byte, 4+len(p)+4)
	binary.BigEndian.PutUint32(rec, uint32(len(p)))
	copy(rec[4:], p)
	binary.BigEndian.PutUint32(rec[4+len(p):], crc32.ChecksumIEEE(p))
	_, err := j.f.Write(rec)
	return err
}

// Close closes the underlying file.
func (j *Journal) Close() error { return j.f.Close() }

// Replay returns the complete records of the log at path. A truncated or
// corrupt tail yields the valid prefix plus a *ReplayError classification.
func Replay(path string) ([]change.Change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReplayBytes(data)
}

// ReplayBytes parses a log image, returning the valid record prefix.
func ReplayBytes(data []byte) ([]change.Change, error) {
	if len(data) < HeaderSize {
		return nil, &ReplayError{Class: ClassHeaderIncomplete, Offset: len(data)}
	}
	if string(data[:4]) != string(magic[:]) {
		return nil, errors.New("journal: bad magic")
	}
	var out []change.Change
	for off := HeaderSize; off < len(data); {
		if off+4 > len(data) {
			return out, &ReplayError{Class: ClassLengthIncomplete, Offset: off}
		}
		n := int(binary.BigEndian.Uint32(data[off:]))
		if off+4+n+4 > len(data) {
			return out, &ReplayError{Class: ClassRecordIncomplete, Offset: off}
		}
		p := data[off+4 : off+4+n]
		if crc32.ChecksumIEEE(p) != binary.BigEndian.Uint32(data[off+4+n:]) {
			return out, &ReplayError{Class: ClassCRCMismatch, Offset: off}
		}
		c, err := change.Decode(p)
		if err != nil {
			return out, err
		}
		out = append(out, c)
		off += 4 + n + 4
	}
	return out, nil
}
