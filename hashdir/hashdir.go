// Package hashdir implements a hash-ordered directory read cursor with
// rename support and previous-generation cursor migration.
package hashdir

import (
	"container/heap"
	"errors"
	"math"
	"sort"
	"sync"
)

// Sentinel rejection reasons, distinguishable with errors.Is.
var (
	ErrInvalidArgument = errors.New("hashdir: invalid argument")
	ErrAlreadyExists   = errors.New("hashdir: name already exists")
	ErrHashFull        = errors.New("hashdir: hash bucket full")
	ErrNotFound        = errors.New("hashdir: name not found")
	ErrStaleCookie     = errors.New("hashdir: stale cookie")
)

// Cookie is a read cursor: (Gen, Pos). Pos == 0 means the beginning and
// skips the generation check.
type Cookie struct {
	Gen uint64
	Pos uint64
}

// Entry is one directory item returned by ReadDir.
type Entry struct {
	Name string
	Ino  uint64
	Key  uint64
}

// Store is a hash-ordered directory. All methods are safe for concurrent
// use; results are equivalent to some serial order.
type Store struct {
	mu sync.Mutex

	seed uint32
	m    uint32
	gen  uint64

	items   map[string]*item
	buckets map[uint32]*bucket
	keys    []uint64 // sorted ascending
	byKey   map[uint64]string

	snapshot  map[string]uint64 // previous-generation name -> key
	snapByKey map[uint64]string
}

type item struct {
	ino   uint64
	h     uint32
	minor uint32
	key   uint64
}

// bucket tracks minor allocation for one hash value.
type bucket struct {
	count int
	next  uint32
	freed uint32Heap
}

// New creates a Store with the given hash seed (generation starts at 1) and
// per-hash occupancy limit m, which must be in [1, 2^31-1].
func New(seed uint32, m uint32) (*Store, error) {
	if m < 1 || m > math.MaxInt32 {
		return nil, ErrInvalidArgument
	}
	return &Store{
		seed:    seed,
		m:       m,
		gen:     1,
		items:   make(map[string]*item),
		buckets: make(map[uint32]*bucket),
		byKey:   make(map[uint64]string),
	}, nil
}

// hashName is FNV-1a seeded by XOR-ing the offset basis with seed; all
// arithmetic wraps modulo 2^32.
func hashName(seed uint32, name string) uint32 {
	h := uint32(2166136261) ^ seed
	for i := 0; i < len(name); i++ {
		h = (h ^ uint32(name[i])) * 16777619
	}
	return h
}

func makeKey(h, minor uint32) uint64 {
	return uint64(h)<<32 | uint64(minor)
}

// alloc returns the smallest non-negative minor not currently used by the
// bucket. Callers must ensure the bucket is not full.
func (b *bucket) alloc() uint32 {
	b.count++
	if len(b.freed) > 0 {
		return heap.Pop(&b.freed).(uint32)
	}
	v := b.next
	b.next++
	return v
}

func (b *bucket) free(minor uint32) {
	b.count--
	heap.Push(&b.freed, minor)
}

func (s *Store) bucketOf(h uint32) *bucket {
	b := s.buckets[h]
	if b == nil {
		b = &bucket{}
		s.buckets[h] = b
	}
	return b
}

// insertLocked inserts name with the given ino, assigning the smallest free
// minor under its hash. Returns ErrHashFull if the hash already has m items.
func (s *Store) insertLocked(name string, ino uint64) (uint64, error) {
	h := hashName(s.seed, name)
	b := s.bucketOf(h)
	if b.count >= int(s.m) {
		return 0, ErrHashFull
	}
	minor := b.alloc()
	key := makeKey(h, minor)
	s.items[name] = &item{ino: ino, h: h, minor: minor, key: key}
	s.byKey[key] = name
	i := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= key })
	s.keys = append(s.keys, 0)
	copy(s.keys[i+1:], s.keys[i:])
	s.keys[i] = key
	return key, nil
}

func (s *Store) removeLocked(name string) {
	it := s.items[name]
	delete(s.items, name)
	delete(s.byKey, it.key)
	i := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= it.key })
	s.keys = append(s.keys[:i], s.keys[i+1:]...)
	b := s.buckets[it.h]
	b.free(it.minor)
	if b.count == 0 {
		delete(s.buckets, it.h)
	}
}

// Add inserts name and returns its key.
func (s *Store) Add(name string, ino uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return 0, ErrInvalidArgument
	}
	if _, ok := s.items[name]; ok {
		return 0, ErrAlreadyExists
	}
	return s.insertLocked(name, ino)
}

// Remove deletes name; its minor becomes reusable immediately.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return ErrInvalidArgument
	}
	if _, ok := s.items[name]; !ok {
		return ErrNotFound
	}
	s.removeLocked(name)
	return nil
}

// Rename keeps the ino, deletes oldName (freeing its minor immediately) and
// re-inserts newName under its own hash with the smallest free minor.
func (s *Store) Rename(oldName, newName string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if oldName == "" || newName == "" {
		return 0, ErrInvalidArgument
	}
	it, ok := s.items[oldName]
	if !ok {
		return 0, ErrNotFound
	}
	if newName == oldName {
		return 0, ErrAlreadyExists
	}
	if _, dup := s.items[newName]; dup {
		return 0, ErrAlreadyExists
	}
	// Simulate "delete old first, then check the new hash bucket" without
	// mutating state, so a rejection leaves everything unchanged.
	hNew := hashName(s.seed, newName)
	count := 0
	if b := s.buckets[hNew]; b != nil {
		count = b.count
	}
	if hNew == it.h {
		count--
	}
	if count >= int(s.m) {
		return 0, ErrHashFull
	}
	ino := it.ino
	s.removeLocked(oldName)
	return s.insertLocked(newName, ino)
}

// ReadDir returns up to n entries with key >= c.Pos in ascending key order,
// the next cookie, and Done (true iff no item has key >= the new cookie's
// Pos). See package docs for the cursor and migration rules.
func (s *Store) ReadDir(c Cookie, n int) ([]Entry, Cookie, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 0 {
		return nil, c, false, ErrInvalidArgument
	}
	pos := c.Pos
	migrated := false
	if c.Pos != 0 && c.Gen != s.gen {
		if c.Gen != s.gen-1 || s.snapshot == nil {
			return nil, c, false, ErrStaleCookie
		}
		q, ok := s.snapByKey[c.Pos-1]
		if !ok {
			return nil, c, false, ErrStaleCookie
		}
		it, ok := s.items[q]
		if !ok {
			return nil, c, false, ErrStaleCookie
		}
		pos = it.key + 1
		migrated = true
	}
	i := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= pos })
	end := i + n
	if end > len(s.keys) {
		end = len(s.keys)
	}
	entries := make([]Entry, 0, end-i)
	for _, key := range s.keys[i:end] {
		name := s.byKey[key]
		entries = append(entries, Entry{Name: name, Ino: s.items[name].ino, Key: key})
	}
	if len(entries) == 0 {
		entries = nil
	}
	next := c
	if len(entries) > 0 {
		next = Cookie{Gen: s.gen, Pos: entries[len(entries)-1].Key + 1}
	} else if migrated {
		next = Cookie{Gen: s.gen, Pos: pos}
	}
	j := sort.Search(len(s.keys), func(j int) bool { return s.keys[j] >= next.Pos })
	done := j == len(s.keys)
	return entries, next, done, nil
}

// CookieOf returns a cursor positioned right after name's current key.
func (s *Store) CookieOf(name string) (Cookie, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return Cookie{}, ErrInvalidArgument
	}
	it, ok := s.items[name]
	if !ok {
		return Cookie{}, ErrNotFound
	}
	return Cookie{Gen: s.gen, Pos: it.key + 1}, nil
}

// Rehash bumps the generation (even if newSeed equals the current seed),
// snapshots the current name->key mapping as the previous generation, and
// re-inserts every item in byte-wise name order under the new seed. If any
// hash would exceed the occupancy limit the whole operation is rejected and
// neither the generation nor the snapshot changes.
func (s *Store) Rehash(newSeed uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.items))
	for name := range s.items {
		names = append(names, name)
	}
	sort.Strings(names)

	type assignment struct {
		h     uint32
		minor uint32
		key   uint64
	}
	newBuckets := make(map[uint32]*bucket)
	assigns := make(map[string]assignment, len(names))
	for _, name := range names {
		h := hashName(newSeed, name)
		b := newBuckets[h]
		if b == nil {
			b = &bucket{}
			newBuckets[h] = b
		}
		if b.count >= int(s.m) {
			return ErrHashFull
		}
		minor := b.alloc()
		assigns[name] = assignment{h: h, minor: minor, key: makeKey(h, minor)}
	}

	s.snapshot = make(map[string]uint64, len(s.items))
	s.snapByKey = make(map[uint64]string, len(s.items))
	for name, it := range s.items {
		s.snapshot[name] = it.key
		s.snapByKey[it.key] = name
	}

	s.gen++
	s.seed = newSeed
	s.buckets = newBuckets
	s.keys = s.keys[:0]
	s.byKey = make(map[uint64]string, len(names))
	for _, name := range names {
		a := assigns[name]
		s.items[name].h = a.h
		s.items[name].minor = a.minor
		s.items[name].key = a.key
		s.byKey[a.key] = name
		s.keys = append(s.keys, a.key)
	}
	sort.Slice(s.keys, func(i, j int) bool { return s.keys[i] < s.keys[j] })
	return nil
}

// Gen returns the current generation.
func (s *Store) Gen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}
