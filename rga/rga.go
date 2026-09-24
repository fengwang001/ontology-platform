// Package rga holds the RGA element tree: insertion by predecessor ID,
// tombstone deletion, the concurrent-insert total order, and visible-text
// traversal. It depends on no other package.
package rga

import "sort"

// ID is a globally unique element identifier: (lamport, replica).
// The zero ID denotes the virtual root ∅.
type ID struct {
	Lamport int
	Replica string
}

// Element is one character node. Del is a tombstone: the node and its
// child links stay in place, only its character becomes invisible.
type Element struct {
	ID   ID
	Prev ID
	Ch   rune
	Del  bool

	kids []ID // sorted by Less, index 0 is nearest to the parent
}

// Less reports whether a sorts before b among concurrent children of the
// same predecessor: higher lamport first; ties broken by higher replica.
// It is a strict total order for distinct IDs.
func Less(a, b ID) bool {
	if a.Lamport != b.Lamport {
		return a.Lamport > b.Lamport
	}
	return a.Replica > b.Replica
}

// Tree is the in-memory RGA element tree.
type Tree struct {
	root  *Element
	elems map[ID]*Element
}

// New returns an empty tree.
func New() *Tree {
	return &Tree{root: &Element{}, elems: make(map[ID]*Element)}
}

// Has reports whether id names an existing (possibly tombstoned) element.
// It is an O(1) hash lookup.
func (t *Tree) Has(id ID) bool {
	_, ok := t.elems[id]
	return ok
}

// Insert places a new element ch with identifier id immediately after prev
// in the concurrent-child order (Less). Caller guarantees id is absent and
// prev is root or an existing element. It returns how many elements were
// examined to locate prev: zero for root, one for a hash hit.
func (t *Tree) Insert(prev, id ID, ch rune) int {
	e := &Element{ID: id, Prev: prev, Ch: ch}
	t.elems[id] = e
	parent := t.root
	checked := 0
	if prev != (ID{}) {
		parent = t.elems[prev]
		checked = 1
	}
	pos := sort.Search(len(parent.kids), func(i int) bool {
		return Less(e.ID, parent.kids[i])
	})
	parent.kids = append(parent.kids, ID{})
	copy(parent.kids[pos+1:], parent.kids[pos:])
	parent.kids[pos] = e.ID
	return checked
}

// Deleted reports whether id is currently tombstoned.
func (t *Tree) Deleted(id ID) bool {
	return t.elems[id].Del
}

// Delete marks id tombstoned without removing it. The node remains a valid
// insertion anchor. Caller guarantees id exists and is alive.
func (t *Tree) Delete(id ID) {
	t.elems[id].Del = true
}

// Text returns the visible text: depth-first traversal from the root,
// children in Less order, tombstoned nodes skipped but their descendants
// still traversed.
func (t *Tree) Text() string {
	var b []rune
	var walk func(*Element)
	walk = func(e *Element) {
		for _, kid := range e.kids {
			c := t.elems[kid]
			if !c.Del {
				b = append(b, c.Ch)
			}
			walk(c)
		}
	}
	walk(t.root)
	return string(b)
}
