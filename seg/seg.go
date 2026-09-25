// Package seg holds the raw segment-tree storage, the +Inf identity
// constant and the node-index helpers. It depends on no other package.
package seg

// Inf is the identity element for minimum: math.MaxInt64.
const Inf = int64(^uint64(0) >> 1)

// Tree is a complete binary tree stored in a 1-indexed slice:
// node 1 is the root; children of node i are 2i and 2i+1.
type Tree struct {
	nodes  []int64
	leaves int
}

// NewTree allocates a tree with the given (power-of-two) number of leaf
// slots; every node is pre-filled with Inf so padding leaves can never
// pollute a minimum.
func NewTree(leaves int) *Tree {
	// leaves real leaves + leaves-1 internal nodes, plus unused slot 0.
	t := &Tree{nodes: make([]int64, 2*leaves), leaves: leaves}
	for i := range t.nodes {
		t.nodes[i] = Inf
	}
	return t
}

// Leaves reports the number of leaf slots.
func (t *Tree) Leaves() int { return t.leaves }

// Leaf returns the storage index of leaf at array position p.
func (t *Tree) Leaf(p int) int { return t.leaves + p }

// At reads node i.
func (t *Tree) At(i int) int64 { return t.nodes[i] }

// Set writes node i.
func (t *Tree) Set(i int, v int64) { t.nodes[i] = v }

// Left returns the index of the left child of node i.
func Left(i int) int { return 2 * i }

// Right returns the index of the right child of node i.
func Right(i int) int { return 2*i + 1 }

// Parent returns the index of the parent of node i (0 for the root).
func Parent(i int) int { return i / 2 }

// Len returns the length of the half-open interval [l, r); zero means empty.
func Len(l, r int) int { return r - l }
