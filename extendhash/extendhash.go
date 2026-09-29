// Package extendhash implements an extendible-hashing bucket-page index.
//
// The directory is indexed by the low bits of an injectable hash function;
// every bucket carries a local depth and is referenced by exactly
// 2^(globalDepth-localDepth) directory entries.
package extendhash

import (
	"errors"
	"sync"
)

// Sentinel errors distinguish every rejection reason.
var (
	// ErrDuplicateKey is returned when inserting a key that already exists.
	ErrDuplicateKey = errors.New("extendhash: duplicate key")
	// ErrNotFound is returned when deleting or looking up a missing key.
	ErrNotFound = errors.New("extendhash: key not found")
	// ErrOverflow is returned when a bucket cannot split any further
	// (local depth reached HashBits) and still cannot fit the key.
	ErrOverflow = errors.New("extendhash: bucket overflow")
	// ErrTooManyBuckets is returned when a split would exceed MaxBuckets.
	ErrTooManyBuckets = errors.New("extendhash: bucket count exceeds limit")
	// ErrClosed is returned for operations on an index that was never
	// initialized or has been torn down.
	ErrClosed = errors.New("extendhash: index closed")
)

// HashFunc maps a key to its hash value. Only the low HashBits bits matter.
type HashFunc[K comparable] func(K) uint64

// Config configures an index.
type Config[K comparable] struct {
	// BucketCap is the fixed maximum number of keys per bucket (>=1).
	BucketCap int
	// HashBits bounds local depth; keys effectively hashing identically
	// over all HashBits low bits cannot be separated by splitting.
	HashBits int
	// MaxBuckets limits the total number of buckets; 0 means unlimited.
	MaxBuckets int
	// Hash is the injectable key hash function.
	Hash HashFunc[K]
}

// Counters reports the cumulative number of structural operations.
type Counters struct {
	Splits  int
	Merges  int
	Doubles int
	Shrinks int
}

// Snapshot is a point-in-time, order-independent view of the index.
type Snapshot[K comparable, V any] struct {
	GlobalDepth int
	Directory   []int // directory[i] is the bucket id selected by low bits i
	Buckets     map[int]BucketSnapshot[K, V]
}

// BucketSnapshot is a copy of one bucket page.
type BucketSnapshot[K comparable, V any] struct {
	LocalDepth int
	Entries    []KVPair[K, V]
}

// KVPair is one key/value pair.
type KVPair[K comparable, V any] struct {
	Key   K
	Value V
}

// bucket is one bucket page.
type bucket[K comparable, V any] struct {
	localDepth int
	entries    []KVPair[K, V]
}

// Index is a concurrency-safe extensible-hashing index.
type Index[K comparable, V any] struct {
	mu          sync.RWMutex
	cfg         Config[K]
	globalDepth int
	directory   []int // directory[i] = bucket id selected by prefix i
	buckets     map[int]*bucket[K, V]
	nextID      int
	counters    Counters
}

// New validates the configuration and returns an empty index with one
// bucket and a single-entry directory (global depth 0).
func New[K comparable, V any](cfg Config[K]) (*Index[K, V], error) {
	if cfg.BucketCap < 1 {
		return nil, errors.New("extendhash: BucketCap must be >= 1")
	}
	if cfg.HashBits < 1 || cfg.HashBits > 64 {
		return nil, errors.New("extendhash: HashBits must be in [1, 64]")
	}
	if cfg.MaxBuckets < 0 {
		return nil, errors.New("extendhash: MaxBuckets must be >= 0")
	}
	if cfg.Hash == nil {
		return nil, errors.New("extendhash: Hash function is required")
	}
	idx := &Index[K, V]{
		cfg:         cfg,
		globalDepth: 0,
		directory:   []int{0},
		buckets: map[int]*bucket[K, V]{
			0: {localDepth: 0, entries: nil},
		},
		nextID: 1,
	}
	return idx, nil
}

// Lookup returns the value associated with key.
func (idx *Index[K, V]) Lookup(key K) (V, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	h := idx.cfg.Hash(key)
	b := idx.buckets[idx.directory[prefix(h, idx.globalDepth)]]
	for _, e := range b.entries {
		if e.Key == key {
			return e.Value, nil
		}
	}
	var zero V
	return zero, ErrNotFound
}

// Insert adds a new key/value pair. Rejected inserts leave the index
// byte-for-byte unchanged.
func (idx *Index[K, V]) Insert(key K, value V) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.insertLocked(key, value)
}

// Delete removes a key. Rejected deletes leave the index unchanged.
func (idx *Index[K, V]) Delete(key K) (V, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.deleteLocked(key)
}

// Snapshot returns a deep copy of the current directory and buckets.
func (idx *Index[K, V]) Snapshot() Snapshot[K, V] {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	snap := Snapshot[K, V]{
		GlobalDepth: idx.globalDepth,
		Directory:   append([]int(nil), idx.directory...),
		Buckets:     make(map[int]BucketSnapshot[K, V], len(idx.buckets)),
	}
	for id, b := range idx.buckets {
		entries := make([]KVPair[K, V], len(b.entries))
		copy(entries, b.entries)
		snap.Buckets[id] = BucketSnapshot[K, V]{LocalDepth: b.localDepth, Entries: entries}
	}
	return snap
}

// Counters returns cumulative structural-operation counts.
func (idx *Index[K, V]) Counters() Counters {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.counters
}
