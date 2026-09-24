// Package hashpart assigns keys to fixed partitions and stores spilled
// partial-aggregation segments with a self-describing header, per-entry
// length prefixes and CRC32 checksums.
package hashpart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
)

const (
	HeaderSize = 16
	version    = 1
	magic      = 0x48535331 // "HPS1"
)

// Sentinel errors classify corruption. They are distinguishable via errors.Is.
var (
	ErrHeader = errors.New("hashpart: incomplete or bad header")
	ErrLength = errors.New("hashpart: incomplete length prefix")
	ErrBody   = errors.New("hashpart: incomplete entry body")
	ErrCRC    = errors.New("hashpart: incomplete or mismatched crc")
	ErrWrite  = errors.New("hashpart: injected write failure")
)

// Error pinpoints which partition and byte offset failed.
type Error struct {
	Part   int
	Offset int64
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("partition %d at offset %d: %v", e.Part, e.Offset, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Partition maps a key to a stable partition in [0,n).
func Partition(key string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

func segName(part, seq int) string {
	return fmt.Sprintf("p%03d-%06d.part", part, seq)
}

// Spiller writes segment files for one aggregation run into Dir.
type Spiller struct {
	Dir    string
	seq    map[int]int
	writes int
	// FailAt > 0 makes the FailAt-th Write call fail (1-based).
	FailAt int
}

// NewSpiller opens dir and removes stale *.tmp files left by crashed runs.
func NewSpiller(dir string) (*Spiller, error) {
	tmp, err := filepath.Glob(filepath.Join(dir, "*.part.tmp"))
	if err != nil {
		return nil, err
	}
	for _, p := range tmp {
		if err := os.Remove(p); err != nil {
			return nil, err
		}
	}
	return &Spiller{Dir: dir, seq: map[int]int{}}, nil
}

// Write appends one spilled segment, writing to a temp name and renaming.
func (s *Spiller) Write(part int, bodies [][]byte) (string, error) {
	s.writes++
	if s.FailAt > 0 && s.writes >= s.FailAt {
		return "", ErrWrite
	}
	buf := make([]byte, HeaderSize)
	binary.LittleEndian.PutUint32(buf[0:], magic)
	buf[4] = version
	binary.LittleEndian.PutUint32(buf[8:], uint32(part))
	binary.LittleEndian.PutUint32(buf[12:], uint32(len(bodies)))
	for _, body := range bodies {
		var lb [4]byte
		binary.LittleEndian.PutUint32(lb[:], uint32(len(body)))
		buf = append(buf, lb[:]...)
		buf = append(buf, body...)
		var cb [4]byte
		binary.LittleEndian.PutUint32(cb[:], crc32.ChecksumIEEE(body))
		buf = append(buf, cb[:]...)
	}
	final := filepath.Join(s.Dir, segName(part, s.seq[part]))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	s.seq[part]++
	return final, nil
}

// Segments returns the number of successfully written segments for part.
func (s *Spiller) Segments(part int) int { return s.seq[part] }

// Cleanup removes every partition file (final and temp) produced in Dir.
func (s *Spiller) Cleanup() error {
	names, err := filepath.Glob(filepath.Join(s.Dir, "p*.part*"))
	if err != nil {
		return err
	}
	for _, p := range names {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ReadPartition reads every segment of part in segment order.
func ReadPartition(dir string, part int) ([][]byte, error) {
	pat := filepath.Join(dir, fmt.Sprintf("p%03d-*.part", part))
	names, err := filepath.Glob(pat)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out [][]byte
	for _, name := range names {
		bodies, err := ReadFile(name, part)
		if err != nil {
			return nil, err
		}
		out = append(out, bodies...)
	}
	return out, nil
}

// ReadFile parses one segment file, classifying every truncation point.
func ReadFile(name string, part int) ([][]byte, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if len(data) < HeaderSize {
		return nil, &Error{Part: part, Offset: int64(len(data)), Err: ErrHeader}
	}
	if binary.LittleEndian.Uint32(data) != magic || data[4] != version ||
		int(binary.LittleEndian.Uint32(data[8:])) != part {
		return nil, &Error{Part: part, Offset: 0, Err: ErrHeader}
	}
	want := int(binary.LittleEndian.Uint32(data[12:]))
	off := HeaderSize
	var out [][]byte
	for i := 0; i < want; i++ {
		if len(data)-off < 4 {
			return nil, &Error{Part: part, Offset: int64(off), Err: ErrLength}
		}
		n := int(binary.LittleEndian.Uint32(data[off : off+4]))
		off += 4
		if len(data)-off < n {
			return nil, &Error{Part: part, Offset: int64(off), Err: ErrBody}
		}
		body := data[off : off+n]
		off += n
		if len(data)-off < 4 {
			return nil, &Error{Part: part, Offset: int64(off), Err: ErrCRC}
		}
		got := binary.LittleEndian.Uint32(data[off : off+4])
		off += 4
		if got != crc32.ChecksumIEEE(body) {
			return nil, &Error{Part: part, Offset: int64(off - 4), Err: ErrCRC}
		}
		out = append(out, append([]byte(nil), body...))
	}
	if len(data) != off {
		return nil, &Error{Part: part, Offset: int64(off), Err: ErrBody}
	}
	return out, nil
}
