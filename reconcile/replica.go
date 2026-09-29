package reconcile

import "sync"

// Replica is one materialized-view copy with incrementally maintained
// range hashes. Key space is [0, keyMax] and at most maxKeys distinct keys
// may be stored.
type Replica struct {
	mu      sync.RWMutex
	fanout  uint64
	keyMax  uint64
	maxKeys int
	values  map[uint64]uint64
	tree    *hashTree
}

// Snapshot is an immutable, point-in-time copy of a replica's content.
// Reconcile reads only snapshots, so every digest interval is field-wise
// consistent with the same point in time.
type Snapshot struct {
	fanout uint64
	keyMax uint64
	values map[uint64]uint64
	tree   *hashTree
}

// NewReplica creates a replica over key space [0, keyMax] partitioned with
// the given fanout. maxKeys bounds the number of distinct stored keys.
func NewReplica(fanout, keyMax uint64, maxKeys int) (*Replica, error) {
	if fanout < 2 {
		return nil, ErrInvalidFanout
	}
	if keyMax < 1 {
		return nil, ErrInvalidKeyMax
	}
	if maxKeys < 1 || uint64(maxKeys) > keyMax+1 {
		return nil, ErrInvalidMaxKeys
	}
	r := &Replica{
		fanout:  fanout,
		keyMax:  keyMax,
		maxKeys: maxKeys,
		values:  make(map[uint64]uint64),
		tree:    newHashTree(fanout, keyMax),
	}
	return r, nil
}

// Put inserts or updates a key. All validation happens before mutation, so
// rejected calls leave keys and range hashes untouched.
func (r *Replica) Put(key, value uint64) error {
	if key > r.keyMax {
		return ErrKeyOutOfRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.values[key]; !exists && len(r.values) >= r.maxKeys {
		return ErrTooManyKeys
	}
	r.values[key] = value
	r.tree.updateLeaf(key, hashLeaf(true, key, value))
	return nil
}

// Delete removes a key; deleting an absent key is a no-op.
func (r *Replica) Delete(key uint64) error {
	if key > r.keyMax {
		return ErrKeyOutOfRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.values[key]; !exists {
		return nil
	}
	delete(r.values, key)
	r.tree.updateLeaf(key, hashLeaf(false, key, 0))
	return nil
}

// Get reports the value and whether the key exists. The boolean, not the
// numeric value, distinguishes an absent key from a present zero-valued key.
func (r *Replica) Get(key uint64) (uint64, bool, error) {
	if key > r.keyMax {
		return 0, false, ErrKeyOutOfRange
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	return value, ok, nil
}

// Len returns the number of stored keys.
func (r *Replica) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.values)
}

// Snapshot returns an immutable point-in-time copy safe for concurrent use
// with later writes to the source replica.
func (r *Replica) Snapshot() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	values := make(map[uint64]uint64, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return &Snapshot{
		fanout: r.fanout,
		keyMax: r.keyMax,
		values: values,
		tree:   cloneTree(r.tree),
	}
}

// Get reports the value and whether the key exists in the snapshot.
func (s *Snapshot) Get(key uint64) (uint64, bool) {
	value, ok := s.values[key]
	return value, ok
}

// Len returns the number of keys in the snapshot.
func (s *Snapshot) Len() int {
	return len(s.values)
}

// RootHash returns the combined digest of the whole key space.
func (s *Snapshot) RootHash() [32]byte {
	return s.tree.levels[0][0]
}
