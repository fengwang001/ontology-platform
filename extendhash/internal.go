package extendhash

// prefix returns the low d bits of h as an int. d must satisfy 0 <= d <= 64.
func prefix(h uint64, d int) int {
	if d >= 64 {
		return int(h)
	}
	return int(h & ((uint64(1) << d) - 1))
}

// hashBit returns bit d (0 = least significant) of h.
func hashBit(h uint64, d int) int {
	return int((h >> d) & 1)
}

// state is a deep copy used to make rejected structural operations atomic.
type state[K comparable, V any] struct {
	globalDepth int
	directory   []int
	buckets     map[int]*bucket[K, V]
	nextID      int
	counters    Counters
}

func (idx *Index[K, V]) saveState() state[K, V] {
	buckets := make(map[int]*bucket[K, V], len(idx.buckets))
	for id, b := range idx.buckets {
		buckets[id] = cloneBucket(b)
	}
	return state[K, V]{
		globalDepth: idx.globalDepth,
		directory:   append([]int(nil), idx.directory...),
		buckets:     buckets,
		nextID:      idx.nextID,
		counters:    idx.counters,
	}
}

func (idx *Index[K, V]) restoreState(s state[K, V]) {
	buckets := make(map[int]*bucket[K, V], len(s.buckets))
	for id, b := range s.buckets {
		buckets[id] = cloneBucket(b)
	}
	idx.globalDepth = s.globalDepth
	idx.directory = append([]int(nil), s.directory...)
	idx.buckets = buckets
	idx.nextID = s.nextID
	idx.counters = s.counters
}

func cloneBucket[K comparable, V any](b *bucket[K, V]) *bucket[K, V] {
	entries := make([]KVPair[K, V], len(b.entries))
	copy(entries, b.entries)
	return &bucket[K, V]{localDepth: b.localDepth, entries: entries}
}

func (idx *Index[K, V]) bucketForHash(h uint64) *bucket[K, V] {
	return idx.buckets[idx.directory[prefix(h, idx.globalDepth)]]
}

// growDirectory doubles the directory; every old entry is mirrored twice.
func (idx *Index[K, V]) growDirectory() {
	old := idx.directory
	next := make([]int, 2*len(old))
	for i, id := range old {
		next[i] = id
		next[i+len(old)] = id
	}
	idx.directory = next
	idx.globalDepth++
	idx.counters.Doubles++
}

// splitBucket replaces bucket bid by two siblings of localDepth+1 and
// redistributes entries by the hash bit at the bucket's old local depth.
func (idx *Index[K, V]) splitBucket(bid int) error {
	b := idx.buckets[bid]
	if idx.cfg.MaxBuckets > 0 && len(idx.buckets) >= idx.cfg.MaxBuckets {
		return ErrTooManyBuckets
	}
	d := b.localDepth
	newID := idx.nextID
	idx.nextID++
	sibling := &bucket[K, V]{localDepth: d + 1}
	b.localDepth = d + 1

	kept := b.entries[:0]
	var moved []KVPair[K, V]
	for _, e := range b.entries {
		if hashBit(idx.cfg.Hash(e.Key), d) == 0 {
			kept = append(kept, e)
		} else {
			moved = append(moved, e)
		}
	}
	b.entries = kept
	sibling.entries = moved
	idx.buckets[newID] = sibling

	for i, id := range idx.directory {
		if id == bid && hashBit(uint64(i), d) == 1 {
			idx.directory[i] = newID
		}
	}
	idx.counters.Splits++
	return nil
}

// insertLocked assumes the write lock is held and performs all splitting /
// doubling against the live state, rolling it back on any rejection.
func (idx *Index[K, V]) insertLocked(key K, value V) error {
	h := idx.cfg.Hash(key)
	target := idx.bucketForHash(h)
	for _, e := range target.entries {
		if e.Key == key {
			return ErrDuplicateKey
		}
	}
	if len(target.entries) < idx.cfg.BucketCap {
		target.entries = append(target.entries, KVPair[K, V]{Key: key, Value: value})
		return nil
	}

	saved := idx.saveState()
	cur := target
	for len(cur.entries) >= idx.cfg.BucketCap {
		if cur.localDepth >= idx.cfg.HashBits {
			idx.restoreState(saved)
			return ErrOverflow
		}
		if cur.localDepth >= idx.globalDepth {
			idx.growDirectory()
		}
		curID := idx.directory[prefix(h, idx.globalDepth)]
		if err := idx.splitBucket(curID); err != nil {
			idx.restoreState(saved)
			return err
		}
		cur = idx.buckets[idx.directory[prefix(h, idx.globalDepth)]]
	}
	// Duplicates cannot have appeared in our own bucket chain: the original
	// bucket held no duplicate, and every split only redistributes its keys,
	// so the new entry is still unique.
	cur.entries = append(cur.entries, KVPair[K, V]{Key: key, Value: value})
	return nil
}

// deleteLocked assumes the write lock is held. Deletes always succeed once the
// key is found; merging and shrinking are unconditional structural follow-ups.
func (idx *Index[K, V]) deleteLocked(key K) (V, error) {
	h := idx.cfg.Hash(key)
	target := idx.buckets[idx.directory[prefix(h, idx.globalDepth)]]
	at := -1
	for i, e := range target.entries {
		if e.Key == key {
			at = i
			break
		}
	}
	if at < 0 {
		var zero V
		return zero, ErrNotFound
	}
	out := target.entries[at].Value
	target.entries = append(target.entries[:at], target.entries[at+1:]...)

	idx.mergeChainFrom(target)
	idx.shrinkWhilePossible()
	return out, nil
}

// mergeChainFrom merges a bucket with its buddy whenever both share a local
// depth and their combined entries fit one bucket; merges may cascade.
func (idx *Index[K, V]) mergeChainFrom(b *bucket[K, V]) {
	for {
		if b.localDepth == 0 {
			return
		}
		bid := ptrBucketID(b, idx)
		d := b.localDepth
		rep := idx.representativeIndex(bid)
		buddyIndex := rep ^ (1 << (d - 1))
		buddyID := idx.directory[buddyIndex]
		if buddyID == bid {
			return // not a real buddy: it is the same (already-merged) bucket
		}
		buddy := idx.buckets[buddyID]
		if buddy.localDepth != d {
			return
		}
		if len(b.entries)+len(buddy.entries) > idx.cfg.BucketCap {
			return
		}

		merged := make([]KVPair[K, V], 0, len(b.entries)+len(buddy.entries))
		merged = append(merged, b.entries...)
		merged = append(merged, buddy.entries...)
		b.entries = merged
		b.localDepth = d - 1
		for i := range idx.directory {
			if idx.directory[i] == buddyID {
				idx.directory[i] = bid
			}
		}
		delete(idx.buckets, buddyID)
		idx.counters.Merges++
	}
}

// ptrBucketID recovers a bucket pointer's id by reverse lookup. Buckets are
// always stored as pointers in the map, so identity comparison works.
func ptrBucketID[K comparable, V any](b *bucket[K, V], idx *Index[K, V]) int {
	for id, cand := range idx.buckets {
		if cand == b {
			return id
		}
	}
	return -1
}

// representativeIndex returns any directory index that maps to bucketID; the
// first such index is used consistently to keep snapshots deterministic.
func (idx *Index[K, V]) representativeIndex(bucketID int) int {
	for i, id := range idx.directory {
		if id == bucketID {
			return i
		}
	}
	return 0
}

// shrinkWhilePossible halves the directory while every bucket's local depth
// is strictly below the global depth.
func (idx *Index[K, V]) shrinkWhilePossible() {
	for idx.globalDepth > 0 {
		can := true
		for _, b := range idx.buckets {
			if b.localDepth >= idx.globalDepth {
				can = false
				break
			}
		}
		if !can {
			return
		}
		half := len(idx.directory) / 2
		for i := 0; i < half; i++ {
			if idx.directory[i] != idx.directory[i+half] {
				return
			}
		}
		idx.directory = idx.directory[:half]
		idx.globalDepth--
		idx.counters.Shrinks++
	}
}
