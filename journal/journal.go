// Package journal is an append-only, self-describing change log with CRC32.
//
// On-disk layout: magic header "ONTJ1\n", then records of
// [4-byte big-endian length n][n-byte JSON payload][4-byte big-endian CRC32].
package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

const magic = "ONTJ1\n"

// Sentinel errors let callers classify truncated / corrupt logs.
var (
	ErrShortHeader = errors.New("journal: incomplete header")
	ErrShortLength = errors.New("journal: incomplete length prefix")
	ErrShortBody   = errors.New("journal: incomplete record body")
	ErrCRC         = errors.New("journal: crc mismatch")
	ErrBadMagic    = errors.New("journal: bad magic header")
)

// Journal is one append-only log file.
type Journal struct {
	f *os.File
}

// Create (or open an existing, valid) journal at path.
func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		if _, err := f.WriteString(magic); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
	} else {
		hdr := make([]byte, len(magic))
		if _, err := io.ReadFull(f, hdr); err != nil {
			f.Close()
			return nil, ErrShortHeader
		}
		if string(hdr) != magic {
			f.Close()
			return nil, ErrBadMagic
		}
	}
	return &Journal{f: f}, nil
}

// Append encodes one change, writes length|payload|crc and fsyncs it.
func (j *Journal) Append(c change.Change) error {
	payload, err := c.Encode()
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(payload))
	if _, err := j.f.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := j.f.Write(payload); err != nil {
		return err
	}
	if _, err := j.f.Write(crcBuf[:]); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close releases the file.
func (j *Journal) Close() error { return j.f.Close() }

// Replay reads every complete record, invoking fn for each. A trailing partial
// record yields a classified sentinel error; all complete records preceding it
// have already been delivered. Decode errors from payloads are returned directly.
func Replay(path string, fn func(change.Change)) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < len(magic) {
		return 0, ErrShortHeader
	}
	if string(data[:len(magic)]) != magic {
		return 0, ErrBadMagic
	}
	pos, count := len(magic), 0
	for pos < len(data) {
		if len(data)-pos < 4 {
			return count, ErrShortLength
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if len(data)-pos < n {
			return count, ErrShortBody
		}
		payload := data[pos : pos+n]
		pos += n
		if len(data)-pos < 4 {
			return count, ErrShortBody
		}
		got := binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
		if got != crc32.ChecksumIEEE(payload) {
			return count, ErrCRC
		}
		c, err := change.Decode(payload)
		if err != nil {
			return count, err
		}
		fn(c)
		count++
	}
	return count, nil
}
