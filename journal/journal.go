// Package journal is the write-ahead change log: an 8-byte self-describing
// header followed by self-committing frames:
//
//	[u32 payloadLen][payload][u32 crc32][u32 commit=payloadLen]
//
// The checksum covers payloadLen‖payload‖commit, so a frame whose commit
// marker is torn off by truncation keeps an intact CRC field but fails the
// checksum (a CRC-class failure), while earlier cuts are incompleteness.
package journal

import (
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

const header = "ONTWAL01"

var (
	// ErrShortHeader: fewer than 8 bytes or a bad magic header.
	ErrShortHeader = errors.New("journal: incomplete or bad header")
	// ErrShortLength: a length prefix is cut mid-frame.
	ErrShortLength = errors.New("journal: incomplete length prefix")
	// ErrShortRecord: payload or CRC bytes are missing.
	ErrShortRecord = errors.New("journal: incomplete record body")
	// ErrCRC: the frame is whole on disk but fails its checksum.
	ErrCRC = errors.New("journal: crc mismatch")
)

// Journal is an appender over one file.
type Journal struct {
	f *os.File
}

// Create makes a fresh journal (truncating any existing file) and writes the
// header, then fsyncs.
func Create(path string) (*Journal, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(header); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f}, nil
}

// Open opens an existing journal for appending (header must be intact).
func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	j := &Journal{f: f}
	if err := j.verifyHeader(); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return j, nil
}

func (j *Journal) verifyHeader() error {
	buf := make([]byte, len(header))
	if _, err := j.f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if string(buf) != header {
		return ErrShortHeader
	}
	return nil
}

// Append encodes, frames, writes and fsyncs one change atomically.
func (j *Journal) Append(c change.Change) error {
	payload := c.Encode(nil)
	plen := len(payload)
	frame := make([]byte, 4+plen+8)
	frame[0] = byte(len(payload))
	frame[1] = byte(len(payload) >> 8)
	frame[2] = byte(len(payload) >> 16)
	frame[3] = byte(len(payload) >> 24)
	copy(frame[4:], payload)
	frame[4+plen+4] = byte(plen)
	frame[4+plen+5] = byte(plen >> 8)
	frame[4+plen+6] = byte(plen >> 16)
	frame[4+plen+7] = byte(plen >> 24)
	sum := crc32.ChecksumIEEE(frame[:4+plen])
	sum = crc32.Update(sum, crc32.IEEETable, frame[4+plen+4:])
	frame[4+plen] = byte(sum)
	frame[4+plen+1] = byte(sum >> 8)
	frame[4+plen+2] = byte(sum >> 16)
	frame[4+plen+3] = byte(sum >> 24)
	if _, err := j.f.Write(frame); err != nil {
		return err
	}
	return j.f.Sync()
}

// Close releases the file.
func (j *Journal) Close() error { return j.f.Close() }

// Replay reads every intact frame, calling fn for each fully decoded record.
// It returns the number of records delivered before the first damaged tail.
func Replay(path string, fn func(change.Change)) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return ReplayBytes(data, fn)
}

// ReplayBytes replays from an in-memory image (used for truncation injection).
func ReplayBytes(data []byte, fn ...func(change.Change)) (int, error) {
	var deliver func(change.Change)
	if len(fn) > 0 {
		deliver = fn[0]
	}
	if len(data) < len(header) || string(data[:len(header)]) != header {
		return 0, ErrShortHeader
	}
	pos := len(header)
	n := 0
	for pos < len(data) {
		if len(data)-pos < 4 {
			return n, ErrShortLength
		}
		plen := int(data[pos]) | int(data[pos+1])<<8 | int(data[pos+2])<<16 | int(data[pos+3])<<24
		crcPos := pos + 4 + plen
		frameEnd := crcPos + 8
		if len(data) < crcPos+4 {
			return n, ErrShortRecord
		}
		stored := uint32(data[crcPos]) | uint32(data[crcPos+1])<<8 |
			uint32(data[crcPos+2])<<16 | uint32(data[crcPos+3])<<24
		if len(data) < frameEnd {
			// CRC field is present but the committed tail it protects is gone.
			return n, ErrCRC
		}
		tail := data[crcPos+4 : frameEnd]
		commit := int(tail[0]) | int(tail[1])<<8 | int(tail[2])<<16 | int(tail[3])<<24
		sum := crc32.Update(crc32.ChecksumIEEE(data[pos:crcPos]), crc32.IEEETable, tail)
		if sum != stored || commit != plen {
			return n, ErrCRC
		}
		c, used, err := change.Decode(data[pos+4 : crcPos])
		if err != nil || used != plen {
			return n, ErrCRC
		}
		if deliver != nil {
			deliver(c)
		}
		n++
		pos = frameEnd
	}
	return n, nil
}
