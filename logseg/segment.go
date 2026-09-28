// Package logseg implements an append-only log segment with a sparse index
// over record positions, allowing fast lookup by target position even when
// positions contain gaps.
package logseg

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Error categories returned by Segment operations. Each rejection maps to a
// distinct sentinel so callers can distinguish causes with errors.Is.
var (
	// ErrInvalidInterval: index interval must be positive.
	ErrInvalidInterval = errors.New("logseg: index interval must be positive")
	// ErrInvalidBasePosition: base position must be non-negative.
	ErrInvalidBasePosition = errors.New("logseg: base position must be non-negative")
	// ErrInvalidSize: record size must be positive.
	ErrInvalidSize = errors.New("logseg: record size must be positive")
	// ErrNonMonotonicPosition: appended position is not strictly increasing.
	ErrNonMonotonicPosition = errors.New("logseg: position not strictly increasing")
	// ErrPositionOverflow: position-basePosition does not fit the index integer.
	ErrPositionOverflow = errors.New("logseg: relative position overflows index integer")
	// ErrBelowBasePosition: lookup target is below the segment base position.
	ErrBelowBasePosition = errors.New("logseg: target below base position")
	// ErrNotFound: no record with position >= target exists in the segment.
	ErrNotFound = errors.New("logseg: no record at or above target")
)

// Record is a single appended entry: a strictly increasing logical position
// and the number of bytes it occupies in the segment.
type Record struct {
	Position int64
	Size     int64
}

// IndexEntry is one sparse-index sample. RelPos is Position-BasePosition
// stored in a bounded integer; PhysOff is the byte offset of the record.
type IndexEntry struct {
	RelPos  int32
	PhysOff int64
	recIdx  int
}

// Segment is an append-only log segment safe for concurrent use.
type Segment struct {
	mu         sync.RWMutex
	basePos    int64
	interval   int64
	records    []Record
	index      []IndexEntry
	totalBytes int64
	sinceEntry int64
	lastPos    int64
	hasRecords bool
}

// NewSegment creates an empty segment. indexIntervalBytes controls how many
// accumulated bytes trigger one index entry.
func NewSegment(basePosition, indexIntervalBytes int64) (*Segment, error) {
	if indexIntervalBytes <= 0 {
		return nil, ErrInvalidInterval
	}
	if basePosition < 0 {
		return nil, ErrInvalidBasePosition
	}
	return &Segment{basePos: basePosition, interval: indexIntervalBytes}, nil
}

// Append validates and appends a record. Any rejection leaves the segment
// completely unchanged.
func (s *Segment) Append(position, size int64) error {
	if size <= 0 {
		return ErrInvalidSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasRecords && position <= s.lastPos {
		return ErrNonMonotonicPosition
	}
	rel := position - s.basePos
	if rel < 0 || rel > math.MaxInt32 {
		return ErrPositionOverflow
	}
	// Decide whether to sample an index entry before writing the record.
	if !s.hasRecords || s.sinceEntry >= s.interval {
		s.index = append(s.index, IndexEntry{
			RelPos:  int32(rel),
			PhysOff: s.totalBytes,
			recIdx:  len(s.records),
		})
		s.sinceEntry = 0
	}
	s.records = append(s.records, Record{Position: position, Size: size})
	s.totalBytes += size
	s.sinceEntry += size
	s.lastPos = position
	s.hasRecords = true
	return nil
}

// Lookup returns the first record whose position is >= target, starting the
// scan from the last index entry not exceeding target.
func (s *Segment) Lookup(target int64) (Record, error) {
	if target < s.basePos {
		return Record{}, ErrBelowBasePosition
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasRecords {
		return Record{}, ErrNotFound
	}
	rel := target - s.basePos
	var start int
	if rel > math.MaxInt32 {
		start = len(s.records) - 1
	} else {
		// Last index entry with RelPos <= rel; the first record is always
		// indexed, so the search never misses.
		i := sort.Search(len(s.index), func(i int) bool {
			return s.index[i].RelPos > int32(rel)
		})
		if i == 0 {
			start = 0
		} else {
			start = s.index[i-1].recIdx
		}
	}
	for ; start < len(s.records); start++ {
		if s.records[start].Position >= target {
			return s.records[start], nil
		}
	}
	return Record{}, ErrNotFound
}

// Index returns a snapshot copy of the sparse index.
func (s *Segment) Index() []IndexEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]IndexEntry, len(s.index))
	copy(out, s.index)
	return out
}

// Len returns the number of records.
func (s *Segment) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// TotalBytes returns the sum of all record sizes.
func (s *Segment) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}
