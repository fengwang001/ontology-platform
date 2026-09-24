// Package wal appends self-describing, CRC-protected batch records to a file.
package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// Magic is both the file header and the per-record marker.
var Magic = [4]byte{'O', 'N', 'T', 'L'}

const (
	// HeaderLen is magic(4)+seq(8)+count(4)+payloadLen(4).
	HeaderLen = 20
	// CRCLen is the trailing batch-level checksum.
	CRCLen = 4
	// FileHeaderLen is the leading file magic.
	FileHeaderLen = 4
)

var (
	// ErrHeaderIncomplete: file shorter than the file header.
	ErrHeaderIncomplete = errors.New("wal: file header incomplete")
	// ErrBatchHeaderIncomplete: fewer than HeaderLen bytes for a record.
	ErrBatchHeaderIncomplete = errors.New("wal: batch header incomplete")
	// ErrEntryIncomplete: header present but entries or CRC truncated.
	ErrEntryIncomplete = errors.New("wal: batch entries incomplete")
	// ErrCRCMismatch: a full-length record whose batch CRC does not match.
	ErrCRCMismatch = errors.New("wal: batch crc mismatch")
)

// Record is one recovered batch: first global sequence and its entries.
type Record struct {
	FirstSeq uint64
	Data     [][]byte
}

func encString(dst []byte, data [][]byte) int {
	off := 0
	for _, d := range data {
		binary.BigEndian.PutUint32(dst[off:], uint32(len(d)))
		off += 4
		off += copy(dst[off:], d)
	}
	return off
}

// PayloadLen returns the encoded byte size of data entries.
func PayloadLen(data [][]byte) int {
	n := 0
	for _, d := range data {
		n += 4 + len(d)
	}
	return n
}

// EncodeRecord produces one on-disk record (header + entries + CRC).
func EncodeRecord(firstSeq uint64, data [][]byte) []byte {
	payload := PayloadLen(data)
	buf := make([]byte, HeaderLen+payload+CRCLen)
	copy(buf, Magic[:])
	binary.BigEndian.PutUint64(buf[4:], firstSeq)
	binary.BigEndian.PutUint32(buf[12:], uint32(len(data)))
	binary.BigEndian.PutUint32(buf[16:], uint32(payload))
	encString(buf[HeaderLen:], data)
	crc := crc32.ChecksumIEEE(buf[4 : HeaderLen+payload])
	binary.BigEndian.PutUint32(buf[HeaderLen+payload:], crc)
	return buf
}

// ParseAt parses one record from the head of b (all remaining file bytes).
// It returns the consumed byte count, or a classifiable sentinel error.
func ParseAt(b []byte) (Record, int, error) {
	if len(b) < HeaderLen {
		return Record{}, 0, ErrBatchHeaderIncomplete
	}
	payload := int(binary.BigEndian.Uint32(b[16:]))
	total := HeaderLen + payload + CRCLen
	if len(b) < total {
		return Record{}, 0, ErrEntryIncomplete
	}
	want := binary.BigEndian.Uint32(b[HeaderLen+payload:])
	if crc32.ChecksumIEEE(b[4:HeaderLen+payload]) != want {
		return Record{}, 0, ErrCRCMismatch
	}
	n := int(binary.BigEndian.Uint32(b[12:]))
	rec := Record{FirstSeq: binary.BigEndian.Uint64(b[4:]), Data: make([][]byte, 0, n)}
	for off := HeaderLen; len(rec.Data) < n; {
		l := int(binary.BigEndian.Uint32(b[off:]))
		off += 4
		if off+l > HeaderLen+payload {
			return Record{}, 0, ErrCRCMismatch
		}
		rec.Data = append(rec.Data, append([]byte(nil), b[off:off+l]...))
		off += l
	}
	return rec, total, nil
}

// Log is the commit-side sink: one Append is one write plus one sync.
type Log interface {
	Append(firstSeq uint64, data [][]byte) error
	io.Closer
}

// FileLog is an OS-file Log. Failure hooks inject crashes for tests.
type FileLog struct {
	mu      sync.Mutex
	f       *os.File
	durable int64
	syncErr error // non-nil: Sync fails with it after a successful write
	partial int    // >0: write only this many bytes then fail (mid-write crash)
}

// Open creates path (writing the file header) or verifies an existing one.
func Open(path string) (*FileLog, error) {
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
		if _, err := f.Write(Magic[:]); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
	} else {
		var h [FileHeaderLen]byte
		if _, err := io.ReadFull(f, h[:]); err != nil || h != Magic {
			f.Close()
			return nil, ErrHeaderIncomplete
		}
	}
	return &FileLog{f: f, durable: FileHeaderLen}, nil
}

// SetSyncError makes the next Sync calls fail with err.
func (l *FileLog) SetSyncError(err error) { l.syncErr = err }

// SetPartialWrite makes the next Append write only n bytes then fail.
func (l *FileLog) SetPartialWrite(n int) { l.partial = n }

// Append writes one record and fsyncs it; partial writes simulate a crash.
func (l *FileLog) Append(firstSeq uint64, data [][]byte) error {
	buf := EncodeRecord(firstSeq, data)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.partial > 0 && l.partial < len(buf) {
		_, _ = l.f.Write(buf[:l.partial])
		l.partial = 0
		return l.abort()
	}
	if _, err := l.f.Write(buf); err != nil {
		return l.abort()
	}
	if l.syncErr != nil {
		err := l.syncErr
		_ = l.abort()
		return err
	}
	if err := l.f.Sync(); err != nil {
		return l.abort()
	}
	l.durable += int64(len(buf))
	return nil
}

// abort rolls the file back to the last known-durable offset so a failed
// batch never leaves a physical tail that later batches could overlap.
func (l *FileLog) abort() error {
	if err := l.f.Truncate(l.durable); err != nil {
		return err
	}
	if _, err := l.f.Seek(l.durable, io.SeekStart); err != nil {
		return err
	}
	return l.f.Sync()
}

// Close flushes and closes the underlying file.
func (l *FileLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
