// Package idx provides a typed zero-based index and re-exports the
// sentinel errors used across the Fenwick tree packages.
package idx

import "ontology/bit"

// Sentinel errors; aliases so errors.Is works against the bit package.
var (
	ErrBadIndex = bit.ErrBadIndex
	ErrBadSize  = bit.ErrBadSize
	ErrBadRange = bit.ErrBadRange
)

// Index is a zero-based position inside a tree.
type Index int

// Tree is a type-safe wrapper around bit.Tree.
type Tree struct {
	b *bit.Tree
}

// New creates a typed tree of n slots.
func New(n int) (*Tree, error) {
	b, err := bit.New(n)
	if err != nil {
		return nil, err
	}
	return &Tree{b: b}, nil
}

// Add adds delta at position i.
func (t *Tree) Add(i Index, delta int64) error { return t.b.Add(int(i), delta) }

// PrefixSum returns the sum over [0, i].
func (t *Tree) PrefixSum(i Index) (int64, error) { return t.b.PrefixSum(int(i)) }

// RangeSum returns the closed-interval sum over [l, r].
func (t *Tree) RangeSum(l, r Index) (int64, error) {
	return t.b.RangeSum(int(l), int(r))
}
