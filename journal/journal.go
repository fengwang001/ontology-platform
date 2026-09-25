// Package journal appends change records to a CRC-protected local log and
// replays them. Frame layout: uint32 body length | body | uint32 CRC32(body).
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

var magic = []byte("JRNLVIEW1") // 8 magic + 1 format version byte

// Truncation / corruption classes, all detectable via errors.Is.
var (
	ErrHeaderIncomplete    = errors.New("journal: header incomplete")
	ErrBadHeader           = errors.New("journal: bad header")
	ErrLenPrefixIncomplete = errors.New("journal: length prefix incomplete")
	ErrBodyIncomplete      = errors.New("journal: record body incomplete")
	ErrCRCMismatch         = errors.New("journal: crc mismatch")
)

const (
	lenPrefixSize = 4
	crcSize       = 4
)

// Writer appends change frames to a journal file.
type Writer struct {
	f *os.File
}

// Create makes a new journal (writing its header) or truncates an existing one.
func Create(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(magic); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Append writes one framed change.
func (w *Writer) Append(c change.Change) error {
	body, err := c.Encode()
	if err != nil {
		return err
	}
	var hdr [lenPrefixSize]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.f.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.f.Write(body); err != nil {
		return err
	}
	var crc [crcSize]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(body))
	if _, err := w.f.Write(crc[:]); err != nil {
		return err
	}
	return nil
}

// Close appends the END marker, then flushes, syncs and closes the log.
// A log missing its END marker (e.g. a torn tail) reports a truncation error.
func (w *Writer) Close() error {
	var hdr [lenPrefixSize]byte // zero-length frame marks END
	var crc [crcSize]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(nil))
	if _, err := w.f.Write(append(append(hdr[:], crc[:]...))); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

func readHeader(r io.Reader) error {
	hdr := make([]byte, len(magic))
	n, err := io.ReadFull(r, hdr)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		if n < len(magic) {
			return ErrHeaderIncomplete
		}
	}
	if err != nil {
		return err
	}
	for i := range magic {
		if hdr[i] != magic[i] {
			return ErrBadHeader
		}
	}
	return nil
}

// Replay reads a journal and invokes fn for every complete, CRC-valid record.
// It returns the number of records delivered and the first tail error
// (nil means a clean end of log). Records after a bad frame are never read.
func Replay(path string, fn func(change.Change) error) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if err := readHeader(f); err != nil {
		return 0, err
	}
	n := 0
	for {
		var hdr [lenPrefixSize]byte
		_, err := io.ReadFull(f, hdr[:])
		if err == io.ErrUnexpectedEOF {
			return n, ErrLenPrefixIncomplete
		}
		if err == io.EOF { // END marker missing: log was truncated mid-tail
			return n, ErrLenPrefixIncomplete
		}
		if err != nil {
			return n, err
		}
		blen := binary.BigEndian.Uint32(hdr[:])
		if blen == 0 { // END marker: consume its CRC and finish cleanly
			var endcrc [crcSize]byte
			if _, err := io.ReadFull(f, endcrc[:]); err != nil {
				return n, ErrCRCMismatch
			}
			if binary.BigEndian.Uint32(endcrc[:]) != crc32.ChecksumIEEE(nil) {
				return n, ErrCRCMismatch
			}
			return n, nil
		}
		body := make([]byte, blen)
		if _, err := io.ReadFull(f, body); err != nil {
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				return n, ErrBodyIncomplete
			}
			return n, err
		}
		var crc [crcSize]byte
		if _, err := io.ReadFull(f, crc[:]); err != nil {
			if err == io.ErrUnexpectedEOF || err == io.EOF {
				// CRC bytes missing: checksum can never match.
				return n, ErrCRCMismatch
			}
			return n, err
		}
		if binary.BigEndian.Uint32(crc[:]) != crc32.ChecksumIEEE(body) {
			return n, ErrCRCMismatch
		}
		c, err := change.Decode(body)
		if err != nil {
			return n, err
		}
		if err := fn(c); err != nil {
			return n, err
		}
		n++
	}
}
