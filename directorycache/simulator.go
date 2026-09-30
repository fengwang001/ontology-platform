// Package directorycache simulates a directory-based cache coherence protocol.
package directorycache

import (
	"container/list"
	"errors"
	"sync"
)

type State int

const (
	// Shared means the cache line may be held by multiple caches and matches memory.
	Shared State = iota
	// Modified means the cache line is the unique writable owner.
	Modified
)

func (s State) String() string {
	switch s {
	case Shared:
		return "shared"
	case Modified:
		return "modified"
	default:
		return "unknown"
	}
}

type Result struct {
	// Value is the value returned by a read or written by a write.
	Value int
	// Hit reports whether the requested block was already cached.
	Hit bool
	// Invalidations is the number of invalidation messages sent.
	Invalidations int
	// EmptyInvalidations is the number of invalidations sent to stale holder entries.
	EmptyInvalidations int
	// Writebacks is the number of dirty values written back to memory.
	Writebacks int
}

type line struct {
	block int
	value int
	state State
	order *list.Element
}

type privateCache struct {
	lines map[int]*line
	order *list.List
}

type directoryEntry struct {
	holders     map[int]struct{}
	modifier    int
	hasModifier bool
}

type Simulator struct {
	mu        sync.Mutex
	count     int
	capacity  int
	caches    []privateCache
	memory    map[int]int
	directory map[int]*directoryEntry
}

var (
	// ErrInvalidCacheCount is returned when the number of caches is not positive.
	ErrInvalidCacheCount = errors.New("cache count must be positive")
	// ErrInvalidCapacity is returned when the private cache capacity is not positive.
	ErrInvalidCapacity = errors.New("cache capacity must be positive")
	// ErrCacheOutOfRange is returned when a cache ID is outside [0,N).
	ErrCacheOutOfRange = errors.New("cache id is out of range")
	// ErrNegativeBlock is returned when a block number is negative.
	ErrNegativeBlock = errors.New("block number must not be negative")
)

// New creates a simulator with cacheCount private caches, each holding at most capacity lines.
func New(cacheCount, capacity int) (*Simulator, error) {
	if cacheCount <= 0 {
		return nil, ErrInvalidCacheCount
	}
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}

	s := &Simulator{
		count:     cacheCount,
		capacity:  capacity,
		caches:    make([]privateCache, cacheCount),
		memory:    make(map[int]int),
		directory: make(map[int]*directoryEntry),
	}
	for i := range s.caches {
		s.caches[i] = privateCache{
			lines: make(map[int]*line),
			order: list.New(),
		}
	}
	return s, nil
}

// Read returns the current value of block in cacheID, performing directory downgrade and fill handling on a miss.
func (s *Simulator) Read(cacheID, block int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(cacheID, block); err != nil {
		return Result{}, err
	}

	cache := &s.caches[cacheID]
	if current, ok := cache.lines[block]; ok {
		cache.touch(current)
		return Result{Value: current.value, Hit: true}, nil
	}

	result := Result{}
	if cache.order.Len() == s.capacity {
		result.Writebacks += s.evict(cacheID, cache)
	}

	entry := s.ensureEntry(block)
	if entry.hasModifier {
		owner := &s.caches[entry.modifier]
		ownerLine := owner.lines[block]
		s.memory[block] = ownerLine.value
		ownerLine.state = Shared
		result.Writebacks++

		entry.holders = map[int]struct{}{
			entry.modifier: {},
			cacheID:        {},
		}
		entry.hasModifier = false
		entry.modifier = 0
	} else {
		entry.holders[cacheID] = struct{}{}
	}

	value := s.memory[block]
	cache.add(block, value, Shared)
	result.Value = value
	return result, nil
}

// Write stores value in block in cacheID, invalidating other directory holders and obtaining exclusive ownership on a miss.
func (s *Simulator) Write(cacheID, block, value int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(cacheID, block); err != nil {
		return Result{}, err
	}

	cache := &s.caches[cacheID]
	if current, ok := cache.lines[block]; ok {
		cache.touch(current)
		if current.state == Modified {
			current.value = value
			return Result{Value: value, Hit: true}, nil
		}
	}

	result := Result{}
	if _, ok := cache.lines[block]; !ok && cache.order.Len() == s.capacity {
		result.Writebacks += s.evict(cacheID, cache)
	}

	entry := s.ensureEntry(block)
	if entry.hasModifier && entry.modifier != cacheID {
		if _, tracked := entry.holders[entry.modifier]; tracked {
			owner := entry.modifier
			ownerCache := &s.caches[owner]
			ownerLine := ownerCache.lines[block]
			s.memory[block] = ownerLine.value
			ownerLine.state = Shared
			result.Writebacks++

			ownerCache.order.Remove(ownerLine.order)
			delete(ownerCache.lines, block)
			result.Invalidations++
		}
	}

	oldHolders := make([]int, 0, len(entry.holders))
	for holder := range entry.holders {
		if holder != cacheID && (!entry.hasModifier || holder != entry.modifier) {
			oldHolders = append(oldHolders, holder)
		}
	}

	for _, holder := range oldHolders {
		holderCache := &s.caches[holder]
		holderLine, exists := holderCache.lines[block]
		if !exists {
			result.EmptyInvalidations++
		} else {
			holderCache.order.Remove(holderLine.order)
			delete(holderCache.lines, block)
		}
		result.Invalidations++
	}

	entry.holders = map[int]struct{}{cacheID: {}}
	entry.modifier = cacheID
	entry.hasModifier = true

	if current, ok := cache.lines[block]; ok {
		current.value = value
		current.state = Modified
		cache.touch(current)
	} else {
		cache.add(block, value, Modified)
	}

	result.Value = value
	return result, nil
}

func (s *Simulator) validate(cacheID, block int) error {
	if cacheID < 0 || cacheID >= s.count {
		return ErrCacheOutOfRange
	}
	if block < 0 {
		return ErrNegativeBlock
	}
	return nil
}

func (s *Simulator) ensureEntry(block int) *directoryEntry {
	entry, ok := s.directory[block]
	if !ok {
		entry = &directoryEntry{holders: make(map[int]struct{})}
		s.directory[block] = entry
	}
	return entry
}

func (s *Simulator) evict(cacheID int, cache *privateCache) int {
	oldest := cache.order.Front()
	victim := oldest.Value.(*line)
	writebacks := 0

	if victim.state == Modified {
		s.memory[victim.block] = victim.value
		entry := s.ensureEntry(victim.block)
		delete(entry.holders, cacheID)
		if entry.hasModifier && entry.modifier == cacheID {
			entry.hasModifier = false
			entry.modifier = 0
		}
		writebacks++
	}

	cache.order.Remove(oldest)
	delete(cache.lines, victim.block)
	return writebacks
}

func (c *privateCache) add(block, value int, state State) {
	current := &line{block: block, value: value, state: state}
	current.order = c.order.PushBack(current)
	c.lines[block] = current
}

func (c *privateCache) touch(current *line) {
	c.order.MoveToBack(current.order)
}
