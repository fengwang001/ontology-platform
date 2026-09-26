// Package api is the public face of the Merkle tree: build, root,
// authentication paths, verification, and a self-check. It depends only
// on package tree.
package api

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/tree"
)

// Decidable sentinel errors, one per rejected operation, all distinct.
var (
	ErrEmptyLeaves     = tree.ErrEmptyLeaves
	ErrNotPowerOfTwo   = tree.ErrNotPowerOfTwo
	ErrIndexOutOfRange = tree.ErrIndexOutOfRange
	ErrRootMismatch    = tree.ErrRootMismatch
	ErrSelfCheck       = errors.New("api: self-check failed")
)

// Tree is a built Merkle tree. Safe for concurrent use.
type Tree struct{ t *tree.Tree }

// Build constructs a tree from leaves (count must be a power of two, >= 1).
// On error no tree exists and no state is kept.
func Build(leaves [][]byte) (*Tree, error) {
	t, err := tree.Build(leaves)
	if err != nil {
		return nil, err
	}
	return &Tree{t: t}, nil
}

// Root returns the root hash as 4 big-endian bytes.
func (t *Tree) Root() [4]byte { return toBytes(t.t.Root()) }

// LeafCount returns the number of leaves.
func (t *Tree) LeafCount() int { return t.t.LeafCount() }

// Proof returns the authentication path for leaf index.
func (t *Tree) Proof(index int) ([][4]byte, error) {
	p, err := t.t.Proof(index)
	if err != nil {
		return nil, err
	}
	out := make([][4]byte, len(p))
	for i, h := range p {
		out[i] = toBytes(h)
	}
	return out, nil
}

// Verify checks that data is the leaf at index given path, comparing the
// reconstructed root against this tree's root.
func (t *Tree) Verify(index int, data []byte, path [][4]byte) (bool, error) {
	p := make([]uint32, len(path))
	for i, h := range path {
		p[i] = fromBytes(h)
	}
	return t.t.Verify(index, data, p)
}

// SelfCheck verifies the four invariants against a built-in 8-leaf tree
// whose root was derived by hand (see NOTES.md). It returns nil when all
// hold, else ErrSelfCheck wrapped with the failing invariant.
func (t *Tree) SelfCheck() error {
	leaves := [][]byte{[]byte("1"), []byte("2"), []byte("3"), []byte("4"),
		[]byte("5"), []byte("6"), []byte("7"), []byte("8")}
	ref, err := Build(leaves)
	if err != nil {
		return fmt.Errorf("%w: build reference: %v", ErrSelfCheck, err)
	}
	const wantRoot = 0x0887B3C1 // hand-derived in NOTES.md, independent of tree.Build
	if fromBytes(ref.Root()) != wantRoot {
		return fmt.Errorf("%w: root 0x%08X != 0x%08X", ErrSelfCheck, fromBytes(ref.Root()), wantRoot)
	}
	for i, d := range leaves { // invariant 1: every proof verifies
		p, err := ref.Proof(i)
		if err != nil {
			return fmt.Errorf("%w: proof(%d): %v", ErrSelfCheck, i, err)
		}
		ok, err := ref.Verify(i, d, p)
		if err != nil || !ok {
			return fmt.Errorf("%w: verify(%d) ok=%v err=%v", ErrSelfCheck, i, ok, err)
		}
	}
	bad := append([]byte(nil), leaves[3]...) // invariant 3: tamper one byte
	bad[0] ^= 0xFF
	p, _ := ref.Proof(3)
	if ok, err := ref.Verify(3, bad, p); ok || !errors.Is(err, ErrRootMismatch) {
		return fmt.Errorf("%w: tampered leaf not rejected", ErrSelfCheck)
	}
	before := ref.Root() // invariant 4: rejections leave state unchanged
	_, e1 := Build(nil)
	_, e2 := Build(leaves[:3])
	_, e3 := ref.Proof(-1)
	_, e4 := ref.Proof(8)
	ok, e5 := ref.Verify(0, []byte("X"), p)
	if e1 == nil || e2 == nil || e3 == nil || e4 == nil || ok || e5 == nil {
		return fmt.Errorf("%w: a rejected operation was accepted", ErrSelfCheck)
	}
	if ref.Root() != before {
		return fmt.Errorf("%w: root changed after rejections", ErrSelfCheck)
	}
	return nil
}

func toBytes(h uint32) [4]byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], h)
	return b
}

func fromBytes(b [4]byte) uint32 { return binary.BigEndian.Uint32(b[:]) }
