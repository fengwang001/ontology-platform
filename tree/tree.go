// Package tree aggregates sampled stacks into a flame-graph call tree.
package tree

import (
	"sort"
	"sync"

	"ontology/stack"
)

// Node is one frame on one call path. Total counts every sample passing
// through; Self counts only samples ending exactly here.
type Node struct {
	Frame     string
	Self      int64
	Total     int64
	Truncated bool // a truncated sample ends at this node
	children  map[string]*Node
}

// Tree is a concurrency-safe aggregate of sampled stacks.
type Tree struct {
	mu               sync.RWMutex
	root             *Node
	samples          int64
	truncatedSamples int64
	insertOps        int64 // comparisons/lookups performed during inserts
}

// New returns an empty tree with a synthetic root.
func New() *Tree { return &Tree{root: &Node{}} }

// Insert merges one normalized stack. Cost is O(stack depth): exactly one
// map lookup per frame, independent of the total number of nodes.
func (t *Tree) Insert(s stack.Stack) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.root
	n.Total++
	for _, f := range s.Frames {
		t.insertOps++
		c, ok := n.children[f]
		if !ok {
			t.insertOps++
			c = &Node{Frame: f}
			if n.children == nil {
				n.children = make(map[string]*Node)
			}
			n.children[f] = c
		}
		c.Total++
		n = c
	}
	n.Self++
	t.samples++
	if s.Truncated {
		n.Truncated = true
		t.truncatedSamples++
	}
}

// Samples returns the number of inserted samples.
func (t *Tree) Samples() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.samples
}

// TruncatedSamples returns how many samples were depth-truncated.
func (t *Tree) TruncatedSamples() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.truncatedSamples
}

// InsertOps exposes the internal insert comparison counter.
func (t *Tree) InsertOps() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.insertOps
}

// SNode is an immutable snapshot node with parent links for attribution.
type SNode struct {
	Frame     string
	Self      int64
	Total     int64
	Truncated bool
	Parent    *SNode
	Children  []*SNode
}

// Snapshot is a consistent, immutable deep copy of a tree.
type Snapshot struct {
	Root             *SNode
	Samples          int64
	TruncatedSamples int64
}

// Snapshot deep-copies the tree under a read lock, so concurrent queries
// never observe a half-updated tree. Children are sorted for determinism.
func (t *Tree) Snapshot() *Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return &Snapshot{
		Root:             copyNode(t.root, nil),
		Samples:          t.samples,
		TruncatedSamples: t.truncatedSamples,
	}
}

func copyNode(n *Node, parent *SNode) *SNode {
	sn := &SNode{
		Frame:     n.Frame,
		Self:      n.Self,
		Total:     n.Total,
		Truncated: n.Truncated,
		Parent:    parent,
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sn.Children = append(sn.Children, copyNode(n.children[name], sn))
	}
	return sn
}

// SelfSum returns the sum of Self over all nodes (identity A: == Samples).
func (s *Snapshot) SelfSum() int64 {
	var sum int64
	walk(s.Root, func(n *SNode) { sum += n.Self })
	return sum
}

// TotalSum returns the sum of Total over all nodes (identity B: > Samples).
func (s *Snapshot) TotalSum() int64 {
	var sum int64
	walk(s.Root, func(n *SNode) { sum += n.Total })
	return sum
}

func walk(n *SNode, f func(*SNode)) {
	f(n)
	for _, c := range n.Children {
		walk(c, f)
	}
}
