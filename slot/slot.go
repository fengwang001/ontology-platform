// Package slot implements a fixed-size ring of ordered doubly-linked lists.
package slot

import "errors"

// ErrBadSize is returned when the ring size is not a positive power of two.
var ErrBadSize = errors.New("slot: ring size must be a positive power of two")

// Node is one entry in a slot list. Callers own Payload and the
// bookkeeping fields; Remove must not be called twice on the same node.
type Node struct {
	next, prev *Node
	list       *Ring
	idx        int // absolute slot index assigned by the caller
	// Payload fields, managed by the wheel package.
	Deadline int64 // absolute deadline in ticks
	Seq      uint64
	Tag      any
	Level    int
	Pos      int // caller bookkeeping (slice / heap index)
}

// Abs returns the absolute ring slot index the node was inserted into.
func (n *Node) Abs() int { return n.idx }

// Ring is a circular array of doubly linked lists.
type Ring struct {
	n     uint
	mask  int
	slots []*Node
}

// New creates a ring of size slots (must be a power of two).
func New(slots int) (*Ring, error) {
	if slots <= 0 || slots&(slots-1) != 0 {
		return nil, ErrBadSize
	}
	return &Ring{n: uint(slots), mask: slots - 1, slots: make([]*Node, slots)}, nil
}

// Size returns the number of slots.
func (r *Ring) Size() int { return int(r.n) }

// PushBack appends node to slot abs, preserving insertion order.
func (r *Ring) PushBack(node *Node, abs int) {
	pos := abs & r.mask
	node.idx = abs
	node.list = r
	if h := r.slots[pos]; h == nil {
		node.prev, node.next = node, node
		r.slots[pos] = node
	} else {
		t := h.prev
		node.prev, node.next = t, h
		t.next = node
		h.prev = node
	}
}

// Remove detaches node from its list in O(1).
func (r *Ring) Remove(node *Node) {
	pos := node.idx & r.mask
	if h := r.slots[pos]; h == node {
		if node.next == node {
			r.slots[pos] = nil
		} else {
			r.slots[pos] = node.next
		}
	}
	node.prev.next = node.next
	node.next.prev = node.prev
	node.next, node.prev, node.list = nil, nil, nil
}

// Drain removes and returns every node in slot abs in insertion order.
// It returns nil when the physical slot is empty or belongs to a
// different (older or newer) wrap of the ring.
func (r *Ring) Drain(abs int) []*Node {
	pos := abs & r.mask
	h := r.slots[pos]
	if h == nil || h.idx != abs {
		return nil
	}
	out := make([]*Node, 0)
	for cur := h; ; {
		nxt := cur.next
		out = append(out, cur)
		if nxt == h {
			break
		}
		cur = nxt
	}
	r.slots[pos] = nil
	for _, n := range out {
		n.next, n.prev, n.list = nil, nil, nil
	}
	return out
}
