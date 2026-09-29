package ontology

import (
	"errors"
	"hash/fnv"
	"sort"
	"sync"
)

var (
	ErrDuplicateKey   = errors.New("duplicate key")
	ErrKeyNotFound    = errors.New("key not found")
	ErrBucketOverflow = errors.New("bucket overflow")
	ErrTooManyBuckets = errors.New("bucket count exceeds limit")
	ErrInvalidConfig  = errors.New("invalid extensible hash index configuration")
)

type HashFunc func(key string) uint64

type Config struct {
	Hash               HashFunc
	Capacity           int
	MaxBuckets         int
	HashBits           uint
	InitialGlobalDepth uint
}

type Counters struct {
	Splits  uint64
	Merges  uint64
	Doubles uint64
	Halves  uint64
}

type BucketSnapshot struct {
	ID         int
	LocalDepth uint
	Keys       []string
}

type Snapshot struct {
	GlobalDepth uint
	Directory   []int
	Buckets     []BucketSnapshot
	Counters    Counters
}

type bucket struct {
	id         int
	localDepth uint
	keys       []string
}

type Index struct {
	mu           sync.RWMutex
	hash         HashFunc
	capacity     int
	maxBuckets   int
	hashBits     uint
	globalDepth  uint
	directory    []int
	buckets      map[int]*bucket
	nextBucketID int
	counters     Counters
}

const maxSupportedHashBits = 63

func New(config Config) (*Index, error) {
	if config.Capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	if config.MaxBuckets <= 0 {
		return nil, ErrInvalidConfig
	}
	if config.HashBits == 0 || config.HashBits > maxSupportedHashBits {
		return nil, ErrInvalidConfig
	}
	if config.InitialGlobalDepth > config.HashBits {
		return nil, ErrInvalidConfig
	}

	hashFunc := config.Hash
	if hashFunc == nil {
		hashFunc = func(key string) uint64 {
			hasher := fnv.New64a()
			_, _ = hasher.Write([]byte(key))
			return hasher.Sum64()
		}
	}

	directory := make([]int, 1<<config.InitialGlobalDepth)
	root := &bucket{id: 1, keys: make([]string, 0, config.Capacity)}
	buckets := map[int]*bucket{root.id: root}
	for i := range directory {
		directory[i] = root.id
	}

	return &Index{
		hash:         hashFunc,
		capacity:     config.Capacity,
		maxBuckets:   config.MaxBuckets,
		hashBits:     config.HashBits,
		globalDepth:  config.InitialGlobalDepth,
		directory:    directory,
		buckets:      buckets,
		nextBucketID: root.id + 1,
	}, nil
}

func (idx *Index) Lookup(key string) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	slot := idx.slotForKey(key, idx.globalDepth)
	bucket := idx.buckets[idx.directory[slot]]
	if containsKey(bucket.keys, key) {
		return nil
	}
	return ErrKeyNotFound
}

func (idx *Index) Insert(key string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	working := idx.cloneState()
	hashValue := idx.hash(key)
	slot := int(hashValue & depthMask(working.globalDepth))
	target := working.buckets[working.directory[slot]]
	if containsKey(target.keys, key) {
		return ErrDuplicateKey
	}

	for {
		slot = int(hashValue & depthMask(working.globalDepth))
		target = working.buckets[working.directory[slot]]
		if len(target.keys) < idx.capacity {
			target.keys = insertSorted(target.keys, key)
			idx.commitState(working)
			return nil
		}

		if target.localDepth == working.globalDepth {
			if working.globalDepth >= idx.hashBits {
				return ErrBucketOverflow
			}
			if len(working.buckets) >= idx.maxBuckets {
				return ErrTooManyBuckets
			}
			working.doubleDirectory()
		} else if len(working.buckets) >= idx.maxBuckets {
			return ErrTooManyBuckets
		}

		if err := working.splitBucket(target.id, idx.hash, idx.capacity, working.globalDepth, slot); err != nil {
			return err
		}
	}
}

func (idx *Index) Delete(key string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	working := idx.cloneState()
	slot := int(idx.hash(key) & depthMask(working.globalDepth))
	target := working.buckets[working.directory[slot]]
	position := sort.SearchStrings(target.keys, key)
	if position == len(target.keys) || target.keys[position] != key {
		return ErrKeyNotFound
	}

	target.keys = append(target.keys[:position], target.keys[position+1:]...)

	for {
		target = working.buckets[working.directory[slot]]
		if target.localDepth == 0 {
			break
		}

		bit := uint64(1) << (target.localDepth - 1)
		buddySlot := slot ^ int(bit)
		buddyID := working.directory[buddySlot]
		if buddyID == target.id {
			break
		}
		buddy := working.buckets[buddyID]
		if buddy.localDepth != target.localDepth {
			break
		}
		if len(target.keys)+len(buddy.keys) > idx.capacity {
			break
		}

		working.mergeBuckets(target.id, buddyID, idx.capacity)
	}

	for working.globalDepth > 0 && working.allBucketsAboveRoot() {
		working.halveDirectory()
	}

	idx.commitState(working)
	return nil
}

func (idx *Index) Stats() Counters {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.counters
}

func (idx *Index) Snapshot() Snapshot {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	buckets := make([]BucketSnapshot, 0, len(idx.buckets))
	for _, current := range idx.buckets {
		keys := make([]string, len(current.keys))
		copy(keys, current.keys)
		buckets = append(buckets, BucketSnapshot{
			ID:         current.id,
			LocalDepth: current.localDepth,
			Keys:       keys,
		})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].ID < buckets[j].ID
	})

	return Snapshot{
		GlobalDepth: idx.globalDepth,
		Directory:   append([]int(nil), idx.directory...),
		Buckets:     buckets,
		Counters:    idx.counters,
	}
}

func (idx *Index) slotForKey(key string, depth uint) int {
	return int(idx.hash(key) & depthMask(depth))
}

func depthMask(depth uint) uint64 {
	return (uint64(1) << depth) - 1
}

func containsKey(keys []string, key string) bool {
	position := sort.SearchStrings(keys, key)
	return position < len(keys) && keys[position] == key
}

func insertSorted(keys []string, key string) []string {
	position := sort.SearchStrings(keys, key)
	keys = append(keys, "")
	copy(keys[position+1:], keys[position:])
	keys[position] = key
	return keys
}

func (idx *Index) cloneState() *Index {
	directory := append([]int(nil), idx.directory...)
	buckets := make(map[int]*bucket, len(idx.buckets))
	for id, current := range idx.buckets {
		keys := append([]string(nil), current.keys...)
		buckets[id] = &bucket{
			id:         current.id,
			localDepth: current.localDepth,
			keys:       keys,
		}
	}
	return &Index{
		hash:         idx.hash,
		capacity:     idx.capacity,
		maxBuckets:   idx.maxBuckets,
		hashBits:     idx.hashBits,
		globalDepth:  idx.globalDepth,
		directory:    directory,
		buckets:      buckets,
		nextBucketID: idx.nextBucketID,
		counters:     idx.counters,
	}
}

func (idx *Index) commitState(working *Index) {
	idx.globalDepth = working.globalDepth
	idx.directory = working.directory
	idx.buckets = working.buckets
	idx.nextBucketID = working.nextBucketID
	idx.counters = working.counters
}

func (idx *Index) doubleDirectory() {
	doubled := make([]int, len(idx.directory)*2)
	copy(doubled, idx.directory)
	copy(doubled[len(idx.directory):], idx.directory)
	idx.directory = doubled
	idx.globalDepth++
	idx.counters.Doubles++
}

func (idx *Index) splitBucket(id int, hash HashFunc, capacity int, globalDepth uint, targetSlot int) error {
	current := idx.buckets[id]
	if current.localDepth >= globalDepth {
		return ErrBucketOverflow
	}

	created := &bucket{
		id:         idx.nextBucketID,
		localDepth: current.localDepth + 1,
		keys:       make([]string, 0, capacity),
	}
	current.localDepth++
	idx.nextBucketID++
	idx.buckets[created.id] = created

	bit := uint64(1) << (current.localDepth - 1)
	currentSide := uint64(targetSlot) & bit
	remaining := make([]string, 0, capacity)
	moving := make([]string, 0, capacity)
	for _, key := range current.keys {
		if hash(key)&bit == currentSide {
			remaining = append(remaining, key)
		} else {
			moving = append(moving, key)
		}
	}
	current.keys = remaining
	created.keys = moving

	for slot, bucketID := range idx.directory {
		if bucketID == current.id && uint64(slot)&bit != currentSide {
			idx.directory[slot] = created.id
		}
	}

	idx.counters.Splits++
	return nil
}

func (idx *Index) mergeBuckets(leftID, rightID int, capacity int) {
	left := idx.buckets[leftID]
	right := idx.buckets[rightID]
	if right.id < left.id {
		left, right = right, left
	}

	mergedKeys := make([]string, 0, capacity)
	mergedKeys = append(mergedKeys, left.keys...)
	mergedKeys = append(mergedKeys, right.keys...)
	sort.Strings(mergedKeys)

	for slot, bucketID := range idx.directory {
		if bucketID == left.id || bucketID == right.id {
			idx.directory[slot] = left.id
		}
	}

	left.localDepth--
	left.keys = mergedKeys
	delete(idx.buckets, right.id)
	idx.counters.Merges++
}

func (idx *Index) allBucketsAboveRoot() bool {
	for _, current := range idx.buckets {
		if current.localDepth >= idx.globalDepth {
			return false
		}
	}
	return true
}

func (idx *Index) halveDirectory() {
	idx.directory = append([]int(nil), idx.directory[:len(idx.directory)/2]...)
	idx.globalDepth--
	idx.counters.Halves++
}
