// Package node is a skip-list node: a key plus, per level, a forward
// pointer and the span (how many bottom-level elements that link skips).
package node

import "ontology/key"

// Node holds one element of the multiset. A nil forward pointer at any
// level always carries span 0: it crosses no elements.
type Node struct {
	Key     key.Key
	forward []*Node
	span    []int
}

// New creates a node with the given key and tower height.
func New(k key.Key, levels int) *Node {
	return &Node{Key: k, forward: make([]*Node, levels), span: make([]int, levels)}
}

// Levels returns the tower height of the node.
func (n *Node) Levels() int { return len(n.forward) }

// Next returns the successor at the given level, or nil.
func (n *Node) Next(level int) *Node { return n.forward[level] }

// SetNext sets the successor at the given level.
func (n *Node) SetNext(level int, next *Node) { n.forward[level] = next }

// Span returns how many bottom-level elements the level link crosses.
func (n *Node) Span(level int) int { return n.span[level] }

// SetSpan sets the span of the level link.
func (n *Node) SetSpan(level int, s int) { n.span[level] = s }
