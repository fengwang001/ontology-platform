// Package overflow implements large-value overflow storage: values larger
// than a byte threshold are spilled to an overflow table and the main record
// keeps only a block-number reference; smaller values are stored inline.
package overflow

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Distinguishable failure reasons; callers test them with errors.Is.
var (
	ErrInvalidThreshold = errors.New("overflow: invalid threshold (must be > 0)")
	ErrInvalidMaxBlocks = errors.New("overflow: invalid max blocks (must be > 0)")
	ErrEmptyKey         = errors.New("overflow: empty key")
	ErrTooManyBlocks    = errors.New("overflow: overflow block limit exceeded")
	ErrKeyNotFound      = errors.New("overflow: key not found")
)

// Record is a main record. When Inline is true, Value holds the data;
// otherwise Block holds the overflow block number. The two are exclusive.
type Record struct {
	Inline bool
	Value  []byte
	Block  uint64
}

// Store is the large-value overflow store. All methods are safe for
// concurrent use.
type Store struct {
	mu        sync.RWMutex
	threshold int
	maxBlocks int
	records   map[string]Record
	blocks    map[uint64][]byte
	nextBlock uint64 // monotonically increasing, never reused
}

// RecoveryReport describes the result of a recovery scan.
type RecoveryReport struct {
	ReclaimedOrphans []uint64 // orphan blocks reclaimed during recovery
	DanglingKeys     []string // keys whose reference points to a missing block
}

// NewStore validates configuration and returns an empty store.
func NewStore(threshold, maxBlocks int) (*Store, error) {
	if threshold <= 0 {
		return nil, ErrInvalidThreshold
	}
	if maxBlocks <= 0 {
		return nil, ErrInvalidMaxBlocks
	}
	return &Store{
		threshold: threshold,
		maxBlocks: maxBlocks,
		records:   make(map[string]Record),
		blocks:    make(map[uint64][]byte),
		nextBlock: 1,
	}, nil
}

// Put inserts or replaces the value for key.
//
// Values of length <= threshold are stored inline in the main record;
// longer values are spilled: a fresh, never-reused block number is
// allocated, the new block is written first, then the reference in the
// main record is updated, and only then is the old block reclaimed. A
// crash between any two of these steps can leave an orphan block but
// never a reference to a missing block; Recover reclaims such orphans.
//
// All validation happens before any mutation, so a rejected Put leaves
// records, blocks and the block counter untouched.
func (s *Store) Put(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old, existed := s.records[key]

	if len(value) <= s.threshold {
		s.records[key] = Record{Inline: true, Value: clone(value)}
		if existed && !old.Inline {
			delete(s.blocks, old.Block)
		}
		return nil
	}

	// Overflow path. The limit is checked before allocating so a
	// rejection changes nothing.
	if len(s.blocks) >= s.maxBlocks {
		return ErrTooManyBlocks
	}
	num := s.nextBlock
	s.blocks[num] = clone(value)                       // 1. write the new block
	s.nextBlock++                                      // block numbers are never reused
	s.records[key] = Record{Inline: false, Block: num} // 2. update the reference
	if existed && !old.Inline {
		delete(s.blocks, old.Block) // 3. reclaim the old block
	}
	return nil
}

// Get returns a copy of the value for key.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key]
	if !ok {
		return nil, false
	}
	if rec.Inline {
		return clone(rec.Value), true
	}
	data, ok := s.blocks[rec.Block]
	if !ok {
		return nil, false // dangling reference; surfaced by Check/Recover
	}
	return clone(data), true
}

// Delete removes key and reclaims its overflow block if any.
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return ErrKeyNotFound
	}
	delete(s.records, key)
	if !rec.Inline {
		delete(s.blocks, rec.Block)
	}
	return nil
}

// Block returns a copy of the overflow block content by block number.
func (s *Store) Block(num uint64) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.blocks[num]
	if !ok {
		return nil, false
	}
	return clone(data), true
}

// Check verifies the reference invariants: every reference points to an
// existing block and every block is referenced by exactly one record.
func (s *Store) Check() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refCount := make(map[uint64]int)
	for key, rec := range s.records {
		if rec.Inline {
			continue
		}
		if _, ok := s.blocks[rec.Block]; !ok {
			return fmt.Errorf("overflow: dangling reference: key %q -> block %d", key, rec.Block)
		}
		refCount[rec.Block]++
	}
	for num := range s.blocks {
		if n := refCount[num]; n != 1 {
			return fmt.Errorf("overflow: block %d referenced %d times, want exactly 1", num, n)
		}
	}
	return nil
}

// Recover scans the store after a crash: it reclaims every orphan block
// (a block no record references) and reports dangling references (records
// whose block no longer exists). The report is sorted so recovery is
// deterministic and reproducible.
func (s *Store) Recover() RecoveryReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rep RecoveryReport
	refCount := make(map[uint64]int)
	for key, rec := range s.records {
		if rec.Inline {
			continue
		}
		if _, ok := s.blocks[rec.Block]; !ok {
			rep.DanglingKeys = append(rep.DanglingKeys, key)
			continue
		}
		refCount[rec.Block]++
	}
	for num := range s.blocks {
		if refCount[num] == 0 {
			delete(s.blocks, num)
			rep.ReclaimedOrphans = append(rep.ReclaimedOrphans, num)
		}
	}
	sort.Slice(rep.ReclaimedOrphans, func(i, j int) bool {
		return rep.ReclaimedOrphans[i] < rep.ReclaimedOrphans[j]
	})
	sort.Strings(rep.DanglingKeys)
	return rep
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
