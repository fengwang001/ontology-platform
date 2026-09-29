package reconcile

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

// Replica is one materialized-view copy over the fixed integer key space
// [0, size). It maintains a fanout-ary summary tree over a regular grid of
// fanout^rootLevel slots; slots in [size, fanout^rootLevel) are permanent
// structural placeholders. Every real node stores a composite hash of its
// interval, so reconciliation drills only into intervals whose digests
// disagree.
type Replica struct {
	mu     sync.RWMutex
	name   string
	size   int
	fanout int
	depth  int

	data map[int][]byte

	// nodeHash caches digests of nodes that cover at least one present key,
	// keyed by grid position (level, lo). Level 0 are single-key leaves.
	nodeHash map[nodeID][32]byte

	normalEmpty     [][32]byte // level -> digest of a fully in-range empty node
	structuralEmpty [][32]byte // level -> digest of a fully out-of-range node
	truncatedEmpty  [][32]byte // level -> digest of the unique node crossing size
}

type nodeID struct {
	level int
	lo    int
}

// NewReplica creates a named replica with key space [0, size) and the given
// partition fanout. Replicas can be reconciled only when size and fanout
// match.
func NewReplica(name string, size, fanout int) (*Replica, error) {
	if size <= 0 {
		return nil, ErrKeySpaceSize
	}
	if fanout < 2 {
		return nil, ErrInvalidFanout
	}
	r := &Replica{
		name:     name,
		size:     size,
		fanout:   fanout,
		data:     make(map[int][]byte),
		nodeHash: make(map[nodeID][32]byte),
	}
	depth := 0
	for stride := 1; stride < size; stride *= fanout {
		depth++
	}
	r.depth = depth
	r.normalEmpty, r.structuralEmpty, r.truncatedEmpty = r.computeBaselines()
	return r, nil
}

// NewReplicaFromData creates a replica and seeds it with initial data in one
// step. It rejects out-of-range keys, nil values and data sets larger than
// the key space before allocating any replica state, so a failed call has no
// observable effect.
func NewReplicaFromData(name string, size, fanout int, data map[int][]byte) (*Replica, error) {
	if size <= 0 {
		return nil, ErrKeySpaceSize
	}
	if fanout < 2 {
		return nil, ErrInvalidFanout
	}
	if len(data) > size {
		return nil, ErrTooManyKeys
	}
	for key, value := range data {
		if key < 0 || key >= size {
			return nil, ErrKeyOutOfRange
		}
		if value == nil {
			return nil, ErrInvalidValue
		}
	}
	r, err := NewReplica(name, size, fanout)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range data {
		stored := make([]byte, len(value))
		copy(stored, value)
		r.data[key] = stored
	}
	for key := range data {
		r.updatePath(key)
	}
	return r, nil
}

// Put stores value under key. nil values are rejected (use Delete); a
// non-nil zero-length value is a present zero-value key, distinct from an
// absent key. Invalid input changes no state.
func (r *Replica) Put(key int, value []byte) error {
	if key < 0 || key >= r.size {
		return ErrKeyOutOfRange
	}
	if value == nil {
		return ErrInvalidValue
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.data[key]; !exists && len(r.data) >= r.size {
		return ErrTooManyKeys
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	r.data[key] = stored
	r.updatePath(key)
	return nil
}

// Delete removes key. Deleting an absent key is a successful no-op.
func (r *Replica) Delete(key int) error {
	if key < 0 || key >= r.size {
		return ErrKeyOutOfRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.data[key]; !exists {
		return nil
	}
	delete(r.data, key)
	r.updatePath(key)
	return nil
}

// Get returns a defensive copy of the value and presence. ok==true with an
// empty slice means a present zero-value key; ok==false means absent.
func (r *Replica) Get(key int) ([]byte, bool, error) {
	if key < 0 || key >= r.size {
		return nil, false, ErrKeyOutOfRange
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.data[key]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out, true, nil
}

func (r *Replica) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.data)
}

func (r *Replica) Name() string { return r.name }
func (r *Replica) Size() int    { return r.size }
func (r *Replica) Fanout() int  { return r.fanout }

// snapshot avoids holding multiple locks while exposing read-only internals
// to the reconciler.
type snapshot struct {
	name            string
	size, fanout    int
	data            map[int][]byte
	nodeHash        map[nodeID][32]byte
	normalEmpty     [][32]byte
	structuralEmpty [][32]byte
	truncatedEmpty  [][32]byte
}

func (r *Replica) snapshotLocked() snapshot {
	return snapshot{
		name:            r.name,
		size:            r.size,
		fanout:          r.fanout,
		data:            r.data,
		nodeHash:        r.nodeHash,
		normalEmpty:     r.normalEmpty,
		structuralEmpty: r.structuralEmpty,
		truncatedEmpty:  r.truncatedEmpty,
	}
}

// updatePath recomputes the leaf of key and every ancestor after a mutation,
// dropping cached nodes whose intervals became empty. Caller holds write
// lock.
func (r *Replica) updatePath(key int) {
	leafID := nodeID{level: 0, lo: key}
	if value, ok := r.data[key]; ok {
		r.nodeHash[leafID] = leafPresentHash(value)
	} else {
		delete(r.nodeHash, leafID)
	}

	stride := 1
	for level := 1; level <= r.depth; level++ {
		stride *= r.fanout
		lo := (key / stride) * stride
		id := nodeID{level: level, lo: lo}
		digest, nonEmpty := r.composeNode(level, lo, stride)
		if nonEmpty {
			r.nodeHash[id] = digest
		} else {
			delete(r.nodeHash, id)
		}
	}
}

// composeNode combines the ordered fanout child digests of grid node
// (level, lo), whose width is stride, and reports whether it covers present
// data. Caller holds r.mu.
func (r *Replica) composeNode(level, lo, stride int) ([32]byte, bool) {
	childStride := stride / r.fanout
	parts := make([][32]byte, r.fanout)
	nonEmpty := false
	for i := 0; i < r.fanout; i++ {
		childLo := lo + i*childStride
		if level-1 == 0 {
			if childLo < r.size {
				if value, ok := r.data[childLo]; ok {
					parts[i] = leafPresentHash(value)
					nonEmpty = true
				} else {
					parts[i] = r.normalEmpty[0]
				}
			} else {
				parts[i] = r.structuralEmpty[0]
			}
			continue
		}
		if digest, ok := r.nodeHash[nodeID{level: level - 1, lo: childLo}]; ok {
			parts[i] = digest
			nonEmpty = true
		} else {
			parts[i] = r.emptyDigestAt(level-1, childLo, childStride)
		}
	}
	return combineNode(level, lo, lo+stride, parts), nonEmpty
}

// emptyDigestAt returns the canonical digest for a node with no present key.
func (r *Replica) emptyDigestAt(level, lo, stride int) [32]byte {
	virtualHi := lo + stride
	switch {
	case virtualHi <= r.size:
		return r.normalEmpty[level]
	case lo >= r.size:
		return r.structuralEmpty[level]
	default:
		return r.truncatedEmpty[level]
	}
}

// nodeDigest returns the canonical digest for any grid node without mutating
// state. Caller holds r.mu (read is enough).
func (r *Replica) nodeDigest(level, lo, stride int) [32]byte {
	if digest, ok := r.nodeHash[nodeID{level: level, lo: lo}]; ok {
		return digest
	}
	return r.emptyDigestAt(level, lo, stride)
}

// computeBaselines fills normal/structural/truncated empty-digest tables for
// levels 0..depth. Each table has at most one distinct node per level
// (truncation boundary is fixed by size), so construction is
// O(depth * fanout).
func (r *Replica) computeBaselines() (normal, structural, truncated [][32]byte) {
	normal = make([][32]byte, r.depth+1)
	structural = make([][32]byte, r.depth+1)
	truncated = make([][32]byte, r.depth+1)
	normal[0] = emptyLeafHash()
	structural[0] = structuralLeafHash()

	stride := 1
	for level := 1; level <= r.depth; level++ {
		stride *= r.fanout
		childStride := stride / r.fanout

		normalChildren := make([][32]byte, r.fanout)
		structChildren := make([][32]byte, r.fanout)
		for i := range normalChildren {
			normalChildren[i] = normal[level-1]
			structChildren[i] = structural[level-1]
		}
		normal[level] = combineNode(level, 0, stride, normalChildren)
		structural[level] = combineNode(level, 0, stride, structChildren)

		crossLo := (r.size / stride) * stride
		children := make([][32]byte, r.fanout)
		for i := 0; i < r.fanout; i++ {
			childLo := crossLo + i*childStride
			childHi := childLo + childStride
			switch {
			case childHi <= r.size:
				children[i] = normal[level-1]
			case childLo >= r.size:
				children[i] = structural[level-1]
			default:
				children[i] = truncated[level-1]
			}
		}
		truncated[level] = combineNode(level, crossLo, crossLo+stride, children)
	}
	return normal, structural, truncated
}

const (
	tagEmptyLeaf      = 1
	tagPresentLeaf    = 2
	tagInternal       = 3
	tagStructuralLeaf = 4
)

func emptyLeafHash() [32]byte {
	return hashOne(tagEmptyLeaf)
}

func structuralLeafHash() [32]byte {
	return hashOne(tagStructuralLeaf)
}

func hashOne(tag byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{tag})
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func leafPresentHash(value []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{tagPresentLeaf})
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	h.Write(length[:])
	h.Write(value)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// combineNode folds node metadata and fanout ordered child digests into one
// composite digest. Fixed-width fields keep framing unambiguous; level/lo/hi
// bind the summary to its grid position.
func combineNode(level, lo, hi int, childDigests [][32]byte) [32]byte {
	h := sha256.New()
	h.Write([]byte{tagInternal})
	var buf [8]byte
	put := func(v int) {
		binary.BigEndian.PutUint64(buf[:], uint64(v))
		h.Write(buf[:])
	}
	put(level)
	put(lo)
	put(hi)
	put(len(childDigests))
	for i := range childDigests {
		h.Write(childDigests[i][:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
