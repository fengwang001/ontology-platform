// Package tree builds a binary Merkle tree over leaf hashes, produces
// authentication paths (Merkle proofs), and verifies them against the root.
package tree

import (
	"errors"
	"sync/atomic"

	"ontology/fnv"
)

// Sentinel errors for every rejected operation; each is distinct so callers
// can decide with errors.Is.
var (
	ErrEmptyLeaves     = errors.New("tree: no leaves")
	ErrNotPowerOfTwo   = errors.New("tree: leaf count is not a power of two")
	ErrIndexOutOfRange = errors.New("tree: index out of range")
	ErrRootMismatch    = errors.New("tree: reconstructed root does not match")
)

// Tree is an immutable Merkle tree. All read methods are safe for
// concurrent use; the only mutable state is the combine counter, guarded
// by atomics.
type Tree struct {
	levels [][]uint32 // levels[0] = leaf hashes, levels[last] = {root}
	// lastCombine records how many Combine calls the most recent Verify
	// executed. Non-exported on purpose: it is observable only from tests
	// in this package, never through the public API.
	lastCombine atomic.Uint32
}

// Build constructs a Merkle tree from leaves. The leaf count must be a
// power of two and at least 1. On error no tree is created.
func Build(leaves [][]byte) (*Tree, error) {
	n := len(leaves)
	if n == 0 {
		return nil, ErrEmptyLeaves
	}
	if n&(n-1) != 0 {
		return nil, ErrNotPowerOfTwo
	}
	level := make([]uint32, n)
	for i, d := range leaves {
		level[i] = fnv.LeafHash(d)
	}
	levels := [][]uint32{level}
	for len(level) > 1 {
		next := make([]uint32, len(level)/2)
		for i := range next {
			next[i] = fnv.Combine(level[2*i], level[2*i+1])
		}
		levels = append(levels, next)
		level = next
	}
	return &Tree{levels: levels}, nil
}

// Root returns the root hash.
func (t *Tree) Root() uint32 { return t.levels[len(t.levels)-1][0] }

// LeafCount returns the number of leaves.
func (t *Tree) LeafCount() int { return len(t.levels[0]) }

// Proof returns the authentication path for leaf index: the sibling hash
// at every level from the leaf layer up to just below the root.
func (t *Tree) Proof(index int) ([]uint32, error) {
	if index < 0 || index >= t.LeafCount() {
		return nil, ErrIndexOutOfRange
	}
	depth := len(t.levels) - 1
	path := make([]uint32, 0, depth)
	for k, i := 0, index; k < depth; k++ {
		path = append(path, t.levels[k][i^1])
		i >>= 1
	}
	return path, nil
}

// Verify checks that data is the leaf at index, given path, by
// reconstructing the root. A wrong index range is ErrIndexOutOfRange; a
// reconstructed root that differs from the tree's root is ErrRootMismatch.
// The tree is never modified.
func (t *Tree) Verify(index int, data []byte, path []uint32) (bool, error) {
	if index < 0 || index >= t.LeafCount() {
		return false, ErrIndexOutOfRange
	}
	depth := len(t.levels) - 1
	if len(path) != depth {
		return false, ErrRootMismatch
	}
	cur := fnv.LeafHash(data)
	n := uint32(0)
	for k, p := range path {
		if (index>>k)&1 == 0 {
			cur = fnv.Combine(cur, p)
		} else {
			cur = fnv.Combine(p, cur)
		}
		n++
	}
	t.lastCombine.Store(n)
	if cur != t.Root() {
		return false, ErrRootMismatch
	}
	return true, nil
}
