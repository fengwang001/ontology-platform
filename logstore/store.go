// Package logstore implements an append-only log-structured store with
// fixed-size segments and a cost-benefit segment cleaner.
package logstore

import (
	"errors"
	"log"
	"sync"
)

// blockHeaderSize is the per-block metadata overhead charged against a segment.
const blockHeaderSize = 16

var (
	ErrEmptyKey       = errors.New("logstore: empty key")
	ErrBlockTooLarge  = errors.New("logstore: block exceeds segment size")
	ErrKeyNotFound    = errors.New("logstore: key not found")
	ErrSpaceExhausted = errors.New("logstore: space exhausted")
	ErrInvalidConfig  = errors.New("logstore: invalid config")
)

// block is a single record appended to a segment. A block is either a data
// block (key/value) or a tombstone marking a deletion.
type block struct {
	key       string
	value     []byte
	tombstone bool
	ts        uint64 // logical clock at original write; preserved by migration
	size      int    // bytes charged against the segment
	seg       int    // slot id of the segment currently holding the block
}

// segment is a fixed-size append-only container occupying one slot.
type segment struct {
	id     int
	blocks []*block
	used   int
	sealed bool
	maxTS  uint64 // write time of the newest block in the segment
}

func (seg *segment) remaining(capacity int) int { return capacity - seg.used }

// Config configures a Store.
type Config struct {
	SegmentSize int         // fixed capacity of every segment, in bytes
	MaxSegments int         // total number of segment slots
	Logger      *log.Logger // optional decision log; nil disables logging
}

// Store is a concurrency-safe log-structured key/value store.
type Store struct {
	mu      sync.RWMutex
	clock   uint64
	segSize int
	slots   []*segment // nil entry = free slot
	current *segment
	index   map[string]*block   // latest op per key
	history map[string][]*block // all on-disk blocks per key, write order
	logger  *log.Logger
}

// New creates a Store. Segment slot 0 becomes the initial current segment.
func New(cfg Config) (*Store, error) {
	if cfg.SegmentSize <= blockHeaderSize || cfg.MaxSegments < 1 {
		return nil, ErrInvalidConfig
	}
	s := &Store{
		segSize: cfg.SegmentSize,
		slots:   make([]*segment, cfg.MaxSegments),
		index:   make(map[string]*block),
		history: make(map[string][]*block),
		logger:  cfg.Logger,
	}
	s.current = s.allocate()
	return s, nil
}

// Put appends a data block for key, superseding any older value.
func (s *Store) Put(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	size := blockHeaderSize + len(key) + len(value)
	if size > s.segSize {
		return ErrBlockTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureSpace(size); err != nil {
		return err
	}
	s.append(&block{key: key, value: value, size: size})
	return nil
}

// Delete appends a tombstone for key.
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	size := blockHeaderSize + len(key)
	if size > s.segSize {
		return ErrBlockTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, ok := s.index[key]
	if !ok || latest.tombstone {
		return ErrKeyNotFound
	}
	if err := s.ensureSpace(size); err != nil {
		return err
	}
	s.append(&block{key: key, tombstone: true, size: size})
	return nil
}

// Get returns the latest value for key.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	latest, ok := s.index[key]
	if !ok || latest.tombstone {
		return nil, false
	}
	return latest.value, true
}

// ensureSpace guarantees the current segment can hold a block of size bytes,
// sealing the current segment and allocating a fresh slot when needed,
// cleaning a victim segment first when no slot is free. On error nothing is
// appended and the current segment is left untouched.
func (s *Store) ensureSpace(size int) error {
	if s.current.remaining(s.segSize) >= size {
		return nil
	}
	if s.freeSlots() == 0 {
		if err := s.cleanOne(); err != nil {
			return err
		}
	}
	s.current.sealed = true
	s.logf("seal segment %d used=%d/%d", s.current.id, s.current.used, s.segSize)
	s.current = s.allocate()
	return nil
}

// append writes a new user block to the current segment, advancing the clock.
func (s *Store) append(b *block) {
	s.clock++
	b.ts = s.clock
	b.seg = s.current.id
	s.current.blocks = append(s.current.blocks, b)
	s.current.used += b.size
	s.current.maxTS = b.ts
	s.index[b.key] = b
	s.history[b.key] = append(s.history[b.key], b)
	s.logf("append key=%q tombstone=%v ts=%d size=%d segment=%d",
		b.key, b.tombstone, b.ts, b.size, b.seg)
}

// blockLive reports whether a block counts toward its segment's live bytes.
//
// A data block is live iff the index points at it (it is the newest op for
// its key). A tombstone is live iff the index points at it AND some older
// data block for the same key still resides on disk in another segment;
// dropping it while such an older block survives would lose the deletion.
func (s *Store) blockLive(b *block) bool {
	if s.index[b.key] != b {
		return false
	}
	if !b.tombstone {
		return true
	}
	for _, older := range s.history[b.key] {
		if older == b {
			break // only blocks written before b matter
		}
		if !older.tombstone && older.seg != b.seg {
			return true
		}
	}
	return false
}

// liveBytes sums the live bytes of a segment.
func (s *Store) liveBytes(seg *segment) int {
	live := 0
	for _, b := range seg.blocks {
		if s.blockLive(b) {
			live += b.size
		}
	}
	return live
}

func (s *Store) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

// allocate takes the lowest-numbered free slot. Callers hold s.mu and must
// ensure a free slot exists.
func (s *Store) allocate() *segment {
	for id := range s.slots {
		if s.slots[id] == nil {
			seg := &segment{id: id}
			s.slots[id] = seg
			return seg
		}
	}
	return nil
}

// freeSlots reports the number of unoccupied slots.
func (s *Store) freeSlots() int {
	n := 0
	for _, seg := range s.slots {
		if seg == nil {
			n++
		}
	}
	return n
}
