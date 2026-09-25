// Package idx provides a typed, 0-based index over the bit package.
package idx

import "ontology/bit"

// Sentinel errors are re-exported from bit so callers can use errors.Is
// without importing the lower-level package.
var (
	ErrBadIndex = bit.ErrBadIndex
	ErrBadSize  = bit.ErrBadSize
	ErrBadRange = bit.ErrBadRange
)

// Index is the public, 0-based slot index. The tree stores it at index+1.
type Index int

// Tree wraps bit.Tree using the typed Index.
type Tree struct {
	b *bit.Tree
}

// New creates a typed tree with n slots.
func New(n int) (*Tree, error) {
	t, err := bit.New(n)
	if err != nil {
		return nil, err
	}
	return &Tree{b: t}, nil
}

// Add adds delta to slot i.
func (t *Tree) Add(i Index, delta int64) error { return t.b.Add(int(i), delta) }

// PrefixSum returns the sum of slots 0..i; PrefixSum(-1) returns 0.
func (t *Tree) PrefixSum(i Index) (int64, error) { return t.b.PrefixSum(int(i)) }

// RangeSum returns the closed-interval sum [l, r].
func (t *Tree) RangeSum(l, r Index) (int64, error) {
	return t.b.RangeSum(int(l), int(r))
}

// Visited reports nodes touched by the last Add or PrefixSum.
func (t *Tree) Visited() int64 { return t.b.Visited() }
