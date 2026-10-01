// Package ihex implements an incremental Intel HEX record loader that
// reconstructs a sparse memory image from textual records.
package ihex

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Rejection reasons, distinguishable with errors.Is.
var (
	ErrSyntax      = errors.New("ihex: syntax error")
	ErrChecksum    = errors.New("ihex: checksum mismatch")
	ErrUnknownType = errors.New("ihex: unknown record type")
	ErrBadField    = errors.New("ihex: LL or AAAA not allowed for record type")
	ErrBoundary    = errors.New("ihex: record crosses 64KiB boundary")
	ErrOverlap     = errors.New("ihex: data overlaps existing image")
	ErrAfterEOF    = errors.New("ihex: line after end-of-file record")
	ErrNoEOF       = errors.New("ihex: finish without end-of-file record")
)

// LineError annotates a rejection with its 1-based line number.
type LineError struct {
	Line int
	Err  error
}

func (e *LineError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Err) }

func (e *LineError) Unwrap() error { return e.Err }

// Segment is a contiguous run of bytes in the sparse image.
type Segment struct {
	Start uint32
	Data  []byte
}

// Loader incrementally accepts Intel HEX records and builds a sparse image.
// It is safe for concurrent use.
type Loader struct {
	mu       sync.Mutex
	lineNo   int
	base     uint32
	start    uint32
	hasStart bool
	eof      bool
	segs     []Segment
}

// NewLoader returns an empty Loader.
func NewLoader() *Loader {
	return &Loader{}
}

// AddLine parses and applies one record line (without trailing newline).
func (l *Loader) AddLine(line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lineNo++
	if err := l.apply(line); err != nil {
		return &LineError{Line: l.lineNo, Err: err}
	}
	return nil
}

// record is a parsed and validated line, ready to be applied.
type record struct {
	typ  byte
	addr uint16
	data []byte
}

// apply validates line in the fixed rejection priority order and, only on
// full success, mutates the loader state.
func (l *Loader) apply(line string) error {
	rec, err := parse(line)
	if err != nil {
		return err
	}
	switch rec.typ {
	case 0x00:
		// Data: boundary check applies even when LL == 0.
		if uint32(rec.addr)+uint32(len(rec.data)) > 0x10000 {
			return ErrBoundary
		}
		if len(rec.data) > 0 && l.overlaps(uint32(rec.addr)+l.base, uint32(len(rec.data))) {
			return ErrOverlap
		}
	case 0x01:
		if len(rec.data) != 0 || rec.addr != 0 {
			return ErrBadField
		}
	case 0x02, 0x04:
		if len(rec.data) != 2 || rec.addr != 0 {
			return ErrBadField
		}
	case 0x03, 0x05:
		if len(rec.data) != 4 || rec.addr != 0 {
			return ErrBadField
		}
	default:
		return ErrUnknownType
	}
	if l.eof {
		return ErrAfterEOF
	}
	switch rec.typ {
	case 0x00:
		if len(rec.data) > 0 {
			l.insert(uint32(rec.addr)+l.base, rec.data)
		}
	case 0x01:
		l.eof = true
	case 0x02:
		l.base = uint32(rec.data[0])<<12 | uint32(rec.data[1])<<4
	case 0x04:
		l.base = uint32(rec.data[0])<<24 | uint32(rec.data[1])<<16
	case 0x03, 0x05:
		l.start = uint32(rec.data[0])<<24 | uint32(rec.data[1])<<16 |
			uint32(rec.data[2])<<8 | uint32(rec.data[3])
		l.hasStart = true
	}
	return nil
}

// parse decodes the line and verifies the checksum. It reports ErrSyntax
// before ErrChecksum.
func parse(line string) (record, error) {
	var rec record
	if len(line) == 0 || line[0] != ':' {
		return rec, ErrSyntax
	}
	hex := line[1:]
	if len(hex)%2 != 0 {
		return rec, ErrSyntax
	}
	raw := make([]byte, len(hex)/2)
	for i := range raw {
		hi, ok1 := hexVal(hex[2*i])
		lo, ok2 := hexVal(hex[2*i+1])
		if !ok1 || !ok2 {
			return rec, ErrSyntax
		}
		raw[i] = hi<<4 | lo
	}
	if len(raw) < 5 {
		return rec, ErrSyntax
	}
	ll := int(raw[0])
	if len(raw) != 5+ll {
		return rec, ErrSyntax
	}
	var sum byte
	for _, b := range raw {
		sum += b
	}
	if sum != 0 {
		return rec, ErrChecksum
	}
	rec.typ = raw[3]
	rec.addr = uint16(raw[1])<<8 | uint16(raw[2])
	rec.data = raw[4 : 4+ll]
	return rec, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Finish reports an error unless an end-of-file record has been seen.
func (l *Loader) Finish() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.eof {
		return &LineError{Line: l.lineNo, Err: ErrNoEOF}
	}
	return nil
}

// Segments returns the image as address-ascending segments with adjacent
// runs merged.
func (l *Loader) Segments() []Segment {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Segment, len(l.segs))
	for i, s := range l.segs {
		out[i] = Segment{Start: s.Start, Data: append([]byte(nil), s.Data...)}
	}
	return out
}

// StartAddress returns the most recent start address from a type 03 or 05
// record.
func (l *Loader) StartAddress() (uint32, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.start, l.hasStart
}

// segEnd returns the exclusive end address of s.
func segEnd(s Segment) uint64 {
	return uint64(s.Start) + uint64(len(s.Data))
}

// overlaps reports whether [start, start+n) intersects any existing segment.
// l.mu must be held.
func (l *Loader) overlaps(start uint32, n uint32) bool {
	end := uint64(start) + uint64(n)
	i := sort.Search(len(l.segs), func(i int) bool { return l.segs[i].Start >= start })
	if i > 0 && segEnd(l.segs[i-1]) > uint64(start) {
		return true
	}
	if i < len(l.segs) && uint64(l.segs[i].Start) < end {
		return true
	}
	return false
}

// insert adds [start, start+len(data)) to the image, merging adjacent
// segments. The caller must have verified there is no overlap. l.mu must be
// held.
func (l *Loader) insert(start uint32, data []byte) {
	i := sort.Search(len(l.segs), func(i int) bool { return l.segs[i].Start >= start })
	l.segs = append(l.segs, Segment{})
	copy(l.segs[i+1:], l.segs[i:])
	l.segs[i] = Segment{Start: start, Data: append([]byte(nil), data...)}
	// Merge with the following segment if they touch.
	if i+1 < len(l.segs) && segEnd(l.segs[i]) == uint64(l.segs[i+1].Start) {
		l.segs[i].Data = append(l.segs[i].Data, l.segs[i+1].Data...)
		l.segs = append(l.segs[:i+1], l.segs[i+2:]...)
	}
	// Merge with the preceding segment if they touch.
	if i > 0 && segEnd(l.segs[i-1]) == uint64(l.segs[i].Start) {
		l.segs[i-1].Data = append(l.segs[i-1].Data, l.segs[i].Data...)
		l.segs = append(l.segs[:i], l.segs[i+1:]...)
	}
}
