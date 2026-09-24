// Package progress persists import progress (written-record intervals with a
// CRC32 trailer), recovers the maximal valid prefix after truncation, and
// provides per-batch lock files and commit markers in a local directory.
package progress

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Truncation/corruption classes, decidable via errors.Is.
var (
	ErrHeader   = errors.New("progress: incomplete header")
	ErrInterval = errors.New("progress: incomplete interval record")
	ErrCRC      = errors.New("progress: CRC mismatch")
	ErrBusy     = errors.New("progress: batch is being imported")
)

// Progress is the recovered/recorded state of one batch.
type Progress struct {
	BatchID   string
	Total     int
	Intervals [][2]int // flushed [start, end) record intervals
}

// Frontier returns the highest flushed interval end.
func (p *Progress) Frontier() int {
	f := 0
	for _, iv := range p.Intervals {
		if iv[1] > f {
			f = iv[1]
		}
	}
	return f
}

// Marshal serializes p with a CRC32 trailer over all preceding bytes.
func Marshal(p *Progress) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "P1 %s %d\n", p.BatchID, p.Total)
	for _, iv := range p.Intervals {
		fmt.Fprintf(&buf, "I %d %d\n", iv[0], iv[1])
	}
	fmt.Fprintf(&buf, "E %08x\n", crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes()
}

// Parse decodes data, returning the maximal recoverable prefix plus a
// classified error (ErrHeader / ErrInterval / ErrCRC), or nil on success.
func Parse(data []byte) (*Progress, error) {
	p := &Progress{}
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return p, ErrHeader
	}
	if _, err := fmt.Sscanf(string(data[:nl]), "P1 %s %d", &p.BatchID, &p.Total); err != nil {
		return p, ErrHeader
	}
	off := nl + 1
	for off < len(data) {
		nl = bytes.IndexByte(data[off:], '\n')
		if nl < 0 { // partial trailing line
			if data[off] == 'I' {
				return p, ErrInterval
			}
			return p, ErrCRC
		}
		line := string(data[off : off+nl])
		switch {
		case strings.HasPrefix(line, "I "):
			var s, e int
			if _, err := fmt.Sscanf(line, "I %d %d", &s, &e); err != nil || s < 0 || e < s {
				return p, ErrInterval
			}
			p.Intervals = append(p.Intervals, [2]int{s, e})
		case strings.HasPrefix(line, "E "):
			var crc uint32
			if _, err := fmt.Sscanf(line, "E %x", &crc); err != nil {
				return p, ErrCRC
			}
			if crc32.ChecksumIEEE(data[:off]) != crc || off+nl+1 != len(data) {
				return p, ErrCRC
			}
			return p, nil
		default:
			return p, ErrCRC
		}
		off += nl + 1
	}
	return p, ErrCRC // missing CRC trailer
}

func path(dir, batchID, kind string) string {
	return filepath.Join(dir, kind+"-"+batchID)
}

// Load reads the progress file; a missing file yields empty progress and no
// error, a truncated file yields the recovered prefix plus a classified error.
func Load(dir, batchID string) (*Progress, error) {
	data, err := os.ReadFile(path(dir, batchID, "progress"))
	if errors.Is(err, os.ErrNotExist) {
		return &Progress{BatchID: batchID}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Save atomically rewrites the progress file (tmp file + rename).
func Save(dir string, p *Progress) error {
	final := path(dir, p.BatchID, "progress")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, Marshal(p), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Acquire takes the batch lock for holder. A live holder yields ErrBusy; an
// expired lock (per the injected clock now and ttl) is taken over safely.
// The returned release func removes the lock file.
func Acquire(dir, batchID, holder string, now time.Time, ttl time.Duration) (func(), error) {
	lockPath := path(dir, batchID, "lock")
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s %d", holder, now.UnixNano())
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		data, rerr := os.ReadFile(lockPath)
		if rerr != nil {
			return nil, rerr
		}
		var h string
		var ts int64
		if _, serr := fmt.Sscanf(string(data), "%s %d", &h, &ts); serr != nil ||
			now.Sub(time.Unix(0, ts)) <= ttl {
			return nil, ErrBusy
		}
		if err := os.Remove(lockPath); err != nil {
			return nil, err
		}
	}
}

// MarkCommitted records the batch as committed.
func MarkCommitted(dir, batchID string) error {
	return os.WriteFile(path(dir, batchID, "committed"), []byte("done\n"), 0o644)
}

// Committed reports whether the batch has a commit marker.
func Committed(dir, batchID string) bool {
	_, err := os.Stat(path(dir, batchID, "committed"))
	return err == nil
}
