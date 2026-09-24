// Package journal appends length-prefixed, CRC32-protected change frames and
// replays them with precise truncation classification.
package journal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"ontology/change"
)

// Magic is the 5-byte self-describing header prefix.
var Magic = []byte("ONTJ\x01")

const headerLen = 13 // 5 magic + 8 declared record count

var (
	// ErrShortHeader means the file header is incomplete or mismatched.
	ErrShortHeader = errors.New("journal: incomplete or bad header")
	// ErrShortLength means a frame's uvarint length prefix is cut off.
	ErrShortLength = errors.New("journal: incomplete length prefix")
	// ErrShortRecord means the body/CRC is cut or declared count is unmet.
	ErrShortRecord = errors.New("journal: incomplete record body")
	// ErrCRC means the frame is whole but the CRC32 does not match.
	ErrCRC = errors.New("journal: CRC mismatch")
)

// Journal is an append-only change log backed by one local file.
type Journal struct {
	f        *os.File
	w        *bufio.Writer
	declared uint64
	n        uint64
}

// Create makes a fresh log that declares expect records will be written.
func Create(dir string, expect uint64) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "view.journal")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h := make([]byte, headerLen)
	copy(h, Magic)
	binary.LittleEndian.PutUint64(h[5:], expect)
	if _, err := f.Write(h); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f, w: bufio.NewWriter(f), declared: expect}, nil
}

// Open opens the log for appending, creating it with declared expect records
// when absent. An existing log must carry a valid self-describing header.
func Open(dir string, expect uint64) (*Journal, error) {
	p := filepath.Join(dir, "view.journal")
	data, err := os.ReadFile(p)
	if err != nil {
		return Create(dir, expect)
	}
	if len(data) < headerLen || string(data[:5]) != string(Magic) {
		return nil, ErrShortHeader
	}
	f, err := os.OpenFile(p, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{f: f, w: bufio.NewWriter(f)}, nil
}

// Append writes one change as a length-prefixed CRC32 frame and fsyncs it.
func (j *Journal) Append(c change.Change) error {
	body := c.Encode()
	var lb [binary.MaxVarintLen64]byte
	l := binary.PutUvarint(lb[:], uint64(len(body)))
	if _, err := j.w.Write(lb[:l]); err != nil {
		return err
	}
	if _, err := j.w.Write(body); err != nil {
		return err
	}
	var cb [4]byte
	binary.LittleEndian.PutUint32(cb[:], crc32.ChecksumIEEE(body))
	if _, err := j.w.Write(cb[:]); err != nil {
		return err
	}
	if err := j.w.Flush(); err != nil {
		return err
	}
	if err := j.f.Sync(); err != nil {
		return err
	}
	j.n++
	return nil
}

// Close flushes and closes the backing file.
func (j *Journal) Close() error {
	if err := j.w.Flush(); err != nil {
		return err
	}
	return j.f.Close()
}

// Replay reads path. want<0 accepts a clean EOF at any frame boundary;
// want>=0 strictly requires that many complete records.
func Replay(path string, want int64) (recs []change.Change, n uint64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < headerLen || string(data[:5]) != string(Magic) {
		return nil, 0, ErrShortHeader
	}
	strict := want >= 0
	if want < 0 {
		want = int64(binary.LittleEndian.Uint64(data[5:13]))
	}
	off := headerLen
	for off < len(data) {
		l, k := binary.Uvarint(data[off:])
		if k <= 0 {
			return recs, n, ErrShortLength
		}
		frame := uint64(k) + l + 4
		if uint64(len(data)-off) < frame {
			return recs, n, ErrShortRecord
		}
		body := data[off+k : off+k+int(l)]
		got := binary.LittleEndian.Uint32(data[off+k+int(l):])
		if crc32.ChecksumIEEE(body) != got {
			return recs, n, ErrCRC
		}
		c, derr := change.Decode(body)
		if derr != nil {
			return recs, n, derr
		}
		recs = append(recs, c)
		n++
		off += int(frame)
	}
	if strict && n != uint64(want) {
		return recs, n, ErrShortRecord
	}
	return recs, n, nil
}
