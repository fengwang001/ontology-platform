// Package segment implements an append-only log segment with a sparse
// offset index for fast offset-based record lookup.
package segment

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// Error categories returned by Segment operations. Each rejection carries a
// distinct, comparable cause so callers can tell failures apart.
var (
	// ErrInvalidRecordSize is returned when a record size is not positive.
	ErrInvalidRecordSize = errors.New("segment: record size must be positive")
	// ErrNonMonotonicOffset is returned when an appended offset is not
	// strictly greater than the previous record's offset.
	ErrNonMonotonicOffset = errors.New("segment: offset not strictly increasing")
	// ErrRelativeOffsetOverflow is returned when the offset relative to the
	// base offset does not fit into the restricted int32 index storage.
	ErrRelativeOffsetOverflow = errors.New("segment: relative offset exceeds int32 range")
	// ErrOffsetBelowBase is returned when an appended or looked-up offset is
	// below the segment base offset.
	ErrOffsetBelowBase = errors.New("segment: offset below base offset")
	// ErrRecordNotFound is returned when no record has an offset greater
	// than or equal to the lookup target.
	ErrRecordNotFound = errors.New("segment: no record with offset >= target")
)

// Record is a single appended log record.
type Record struct {
	Offset   int64 // absolute offset (position) of the record
	Size     int64 // number of bytes the record occupies
	Position int64 // physical position: sum of all previous record sizes
}

// Entry is a sparse index entry. RelativeOffset is stored as a restricted
// int32; Position is the physical byte position of the indexed record.
type Entry struct {
	RelativeOffset int32
	Position       int64
}

// entry is the internal index row; it additionally tracks the record index
// so lookups can jump straight into the record slice.
type entry struct {
	Entry
	recordIndex int
}

// Segment is an append-only log segment with a sparse offset index.
// It is safe for concurrent use by multiple goroutines.
type Segment struct {
	mu              sync.RWMutex
	baseOffset      int64
	intervalBytes   int64
	records         []Record
	entries         []entry
	totalBytes      int64
	bytesSinceIndex int64
	lastOffset      int64
	hasRecords      bool
}

// NewSegment creates an empty segment whose offsets start at baseOffset.
// An index entry is recorded every time at least intervalBytes have
// accumulated since the previous entry. intervalBytes must be positive.
func NewSegment(baseOffset, intervalBytes int64) (*Segment, error) {
	if intervalBytes <= 0 {
		return nil, fmt.Errorf("segment: index interval must be positive: %w", ErrInvalidRecordSize)
	}
	return &Segment{baseOffset: baseOffset, intervalBytes: intervalBytes}, nil
}

// Append validates and appends a record occupying size bytes at the given
// absolute offset. It reports whether a new index entry was recorded.
//
// The decision to record an index entry is made before the record is
// written: an entry {relativeOffset, position} is added whenever at least
// intervalBytes have accumulated since the previous entry. Any rejection
// leaves records, index and byte counters untouched.
func (s *Segment) Append(offset, size int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if size <= 0 {
		return false, fmt.Errorf("append offset=%d size=%d: %w", offset, size, ErrInvalidRecordSize)
	}
	if offset < s.baseOffset {
		return false, fmt.Errorf("append offset=%d base=%d: %w", offset, s.baseOffset, ErrOffsetBelowBase)
	}
	if s.hasRecords && offset <= s.lastOffset {
		return false, fmt.Errorf("append offset=%d last=%d: %w", offset, s.lastOffset, ErrNonMonotonicOffset)
	}
	relative := offset - s.baseOffset
	if relative > math.MaxInt32 {
		return false, fmt.Errorf("append offset=%d relative=%d: %w", offset, relative, ErrRelativeOffsetOverflow)
	}

	indexed := false
	if s.bytesSinceIndex >= s.intervalBytes {
		s.entries = append(s.entries, entry{
			Entry:       Entry{RelativeOffset: int32(relative), Position: s.totalBytes},
			recordIndex: len(s.records),
		})
		s.bytesSinceIndex = 0
		indexed = true
	}

	s.records = append(s.records, Record{Offset: offset, Size: size, Position: s.totalBytes})
	s.totalBytes += size
	s.bytesSinceIndex += size
	s.lastOffset = offset
	s.hasRecords = true
	return indexed, nil
}

// Lookup returns the first record whose offset is greater than or equal to
// target, matching the result of a naive scan from the segment start.
//
// It binary-searches the sparse index for the last entry whose relative
// offset does not exceed the target, then scans forward from that entry's
// physical position.
func (s *Segment) Lookup(target int64) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lookupLocked(target)
}

// lookupLocked performs the lookup with the caller holding s.mu (read or
// write lock).
func (s *Segment) lookupLocked(target int64) (Record, error) {
	if target < s.baseOffset {
		return Record{}, fmt.Errorf("lookup target=%d base=%d: %w", target, s.baseOffset, ErrOffsetBelowBase)
	}

	start := 0
	if i := s.floorEntry(target - s.baseOffset); i >= 0 {
		start = s.entries[i].recordIndex
	}
	for i := start; i < len(s.records); i++ {
		if s.records[i].Offset >= target {
			return s.records[i], nil
		}
	}
	return Record{}, fmt.Errorf("lookup target=%d: %w", target, ErrRecordNotFound)
}

// floorEntry returns the index of the last entry whose RelativeOffset is
// less than or equal to relativeTarget, or -1 if there is none.
func (s *Segment) floorEntry(relativeTarget int64) int {
	lo, hi := 0, len(s.entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if int64(s.entries[mid].RelativeOffset) <= relativeTarget {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// Records returns a copy of all appended records in append order.
func (s *Segment) Records() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Entries returns a copy of the current sparse index entries.
func (s *Segment) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	for i, e := range s.entries {
		out[i] = e.Entry
	}
	return out
}

// TotalBytes returns the total number of bytes occupied by all records.
func (s *Segment) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}

// BaseOffset returns the segment base offset.
func (s *Segment) BaseOffset() int64 {
	return s.baseOffset
}

// ExpectedEntries recomputes the sparse index that the append rules produce
// for the given records and interval. It is used to verify that a segment's
// index matches a from-scratch recomputation.
func ExpectedEntries(records []Record, baseOffset, intervalBytes int64) []Entry {
	var out []Entry
	var bytesSinceIndex int64
	for _, r := range records {
		if bytesSinceIndex >= intervalBytes {
			out = append(out, Entry{RelativeOffset: int32(r.Offset - baseOffset), Position: r.Position})
			bytesSinceIndex = 0
		}
		bytesSinceIndex += r.Size
	}
	return out
}
