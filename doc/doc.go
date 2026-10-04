package doc

import (
	"errors"
	"sort"
)

var ErrInvalid = errors.New("doc: invalid argument")

// ErrTooLarge is returned by Merge when the merged document would hold more
// than the leaf limit.
var ErrTooLarge = errors.New("doc: too many leaves")

type Kind int

const (
	Object Kind = iota
	Int
	Str
	Bool
	NullKind
)

// Node is a document node: either an object (Kids != nil) or a typed leaf.
// Null nodes are only legal inside a Patch and request key deletion.
type Node struct {
	Kind Kind
	I    int64
	S    string
	B    bool
	Kids map[string]*Node
	// nleaf is the number of leaves in this subtree, maintained by Merge.
	nleaf int
}

// Patch is a validated merge patch. Its own shape (not the merge result)
// decides validity, so it is checked once at construction time.
type Patch struct {
	root *Node
}

const (
	maxKeyBytes = 64
	maxDepth    = 4
)

func NewObject() *Node          { return &Node{Kind: Object, Kids: map[string]*Node{}} }
func NullNode() *Node           { return &Node{Kind: NullKind} }
func IntLeaf(v int64) *Node     { return &Node{Kind: Int, I: v, nleaf: 1} }
func StringLeaf(v string) *Node { return &Node{Kind: Str, S: v, nleaf: 1} }
func BoolLeaf(v bool) *Node     { return &Node{Kind: Bool, B: v, nleaf: 1} }

// Obj builds an object node from alternating key/value pairs (test helper).
func Obj(kv ...any) *Node {
	n := NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		n.Kids[kv[i].(string)] = kv[i+1].(*Node)
	}
	return n
}

// Hooks observe leaf-level changes after a change has been proven real.
// Set fires when a leaf appears or its value/type changes.
// Delete fires when a leaf or a whole subtree disappears.
type Hooks struct {
	Set    func(path string, old, leaf *Node)
	Delete func(path string, old *Node)
}

// Result reports what a merge did. Touched counts pre-existing document nodes
// examined, replaced or removed; it never depends on unmentioned subtrees.
type Result struct {
	Changed bool
	Touched int
	Leaves  int
}

// Merge applies a validated patch to the root object dst. *leaves must hold the
// current leaf count. The limit is checked against the committed post-merge
// count per top-level key before any mutation, so an over-limit rejection
// never touches state.
func Merge(dst *Node, p Patch, leaves *int, limit int, hooks Hooks) (Result, error) {
	if dst == nil || dst.Kind != Object {
		return Result{}, ErrInvalid
	}
	res := Result{Leaves: *leaves}
	diff := 0
	for k, v := range p.root.Kids {
		old := dst.Kids[k]
		diff += resultLeaves(old, v) - leavesOf(old)
	}
	if limit > 0 && *leaves+diff > limit {
		return res, ErrTooLarge
	}
	for k, v := range p.root.Kids {
		d, touched, err := apply(dst, k, v, k, hooks, &res)
		if err != nil {
			return res, err
		}
		dst.nleaf += d
		res.Touched += touched
	}
	*leaves = dst.nleaf
	res.Leaves = dst.nleaf
	return res, nil
}

// resultLeaves returns the leaf count of a subtree after merging patch pn over
// old (0 means the key disappears). Pure: it descends only into patch keys.
func resultLeaves(old, pn *Node) int {
	switch pn.Kind {
	case NullKind:
		return 0
	case Int, Str, Bool:
		return 1
	}
	var base *Node
	if old != nil && old.Kind == Object {
		base = old
	}
	c := 0
	seen := make(map[string]bool, len(pn.Kids))
	for k := range pn.Kids {
		seen[k] = true
	}
	if base != nil {
		for k, ov := range base.Kids {
			if !seen[k] {
				c += ov.nleaf
			}
		}
	}
	for k, pv := range pn.Kids {
		var ov *Node
		if base != nil {
			ov = base.Kids[k]
		}
		c += resultLeaves(ov, pv)
	}
	return c
}

// apply merges one key into parent. It returns the leaf-count delta and the
// number of document nodes touched (visited/created/replaced/removed).
func apply(parent *Node, key string, pn *Node, path string, h Hooks, res *Result) (int, int, error) {
	old := parent.Kids[key]
	switch pn.Kind {
	case NullKind:
		if old != nil {
			touched := detach(parent, key, path, old, h, res)
			return -leavesOf(old), touched, nil
		}
		return 0, 0, nil
	case Int, Str, Bool:
		switch {
		case old == nil:
			nw := leafOf(pn)
			parent.Kids[key] = nw
			if h.Set != nil {
				h.Set(path, nil, nw)
			}
			res.Changed = true
			return 1, 1, nil
		case old.Kind == pn.Kind && leafEqualValue(old, pn):
			return 0, 1, nil
		case old.Kind == Object:
			if h.Delete != nil {
				h.Delete(path, old)
			}
			nw := leafOf(pn)
			parent.Kids[key] = nw
			if h.Set != nil {
				h.Set(path, nil, nw)
			}
			res.Changed = true
			return 1 - leavesOf(old), Size(old) + 1, nil
		default:
			nw := leafOf(pn)
			if h.Set != nil {
				h.Set(path, old, nw)
			}
			parent.Kids[key] = nw
			res.Changed = true
			return 0, 1, nil
		}
	case Object:
		switch {
		case old != nil && old.Kind == Object:
			before := old.nleaf
			delta := 0
			touched := 1
			for ck, cv := range pn.Kids {
				cd, ct, err := apply(old, ck, cv, join(path, ck), h, res)
				if err != nil {
					return 0, 0, err
				}
				old.nleaf += cd
				delta += cd
				touched += ct
			}
			if len(pn.Kids) > 0 && len(old.Kids) == 0 {
				touched += detach(parent, key, path, old, h, res)
				return -before, touched, nil
			}
			return delta, touched, nil
		default:
			fresh := NewObject()
			delta := 0
			touched := 0
			for ck, cv := range pn.Kids {
				cd, ct, err := apply(fresh, ck, cv, join(path, ck), h, res)
				if err != nil {
					return 0, 0, err
				}
				fresh.nleaf += cd
				delta += cd
				touched += ct
			}
			if len(fresh.Kids) == 0 {
				if old != nil {
					t := detach(parent, key, path, old, h, res)
					return delta - leavesOf(old), t, nil
				}
				return 0, 0, nil
			} else {
				touched++
				if old != nil {
					if h.Delete != nil {
						h.Delete(path, old)
					}
					touched++
				}
				parent.Kids[key] = fresh
				return fresh.nleaf - leavesOf(old), touched, nil
			}
		}
	}
	return 0, 0, nil
}

func detach(parent *Node, key, path string, old *Node, h Hooks, res *Result) int {
	if h.Delete != nil {
		h.Delete(path, old)
	}
	delete(parent.Kids, key)
	res.Changed = true
	return Size(old)
}

func leafOf(n *Node) *Node {
	return &Node{Kind: n.Kind, I: n.I, S: n.S, B: n.B, nleaf: 1}
}

func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func leafEqualValue(a, b *Node) bool {
	switch a.Kind {
	case Int:
		return a.I == b.I
	case Str:
		return a.S == b.S
	case Bool:
		return a.B == b.B
	}
	return false
}

// EqualLeaves reports whether two nodes are the same typed leaf.
func EqualLeaves(a, b *Node) bool {
	if a == nil || b == nil || a.Kind == Object || b.Kind == Object || a.Kind != b.Kind {
		return false
	}
	return leafEqualValue(a, b)
}

// Size counts all nodes in a subtree.
func Size(n *Node) int {
	if n == nil {
		return 0
	}
	if n.Kind != Object {
		return 1
	}
	c := 1
	for _, k := range n.Kids {
		c += Size(k)
	}
	return c
}

func leavesOf(n *Node) int {
	if n == nil {
		return 0
	}
	return n.nleaf
}

// Leaves reports the maintained leaf count of a subtree.
func Leaves(n *Node) int {
	if n == nil {
		return 0
	}
	return n.nleaf
}

// RebuildCounts recomputes every node's cached leaf count.
func RebuildCounts(n *Node) {
	if n == nil || n.Kind != Object {
		return
	}
	c := 0
	for _, ch := range n.Kids {
		switch ch.Kind {
		case Object:
			RebuildCounts(ch)
			c += ch.nleaf
		default:
			ch.nleaf = 1
			c++
		}
	}
	n.nleaf = c
}

// Clone deep-copies a subtree.
func Clone(n *Node) *Node {
	if n == nil {
		return nil
	}
	c := *n
	if n.Kind == Object {
		c.Kids = make(map[string]*Node, len(n.Kids))
		for k, v := range n.Kids {
			c.Kids[k] = Clone(v)
		}
	}
	return &c
}

// NewPatch validates a patch: the root must be a non-empty object, every key
// must be 1..64 bytes without '.', and no key path may exceed 4 segments.
// Depth is judged by the patch shape alone, null or empty-object branches
// included.
func NewPatch(root *Node) (Patch, error) {
	if root == nil || root.Kind != Object || len(root.Kids) == 0 {
		return Patch{}, ErrInvalid
	}
	if err := validate(root, 0); err != nil {
		return Patch{}, err
	}
	return Patch{root: root}, nil
}

func validate(n *Node, depth int) error {
	for k, ch := range n.Kids {
		if len(k) == 0 || len(k) > maxKeyBytes {
			return ErrInvalid
		}
		for i := 0; i < len(k); i++ {
			if k[i] == '.' {
				return ErrInvalid
			}
		}
		if depth+1 > maxDepth || ch == nil {
			return ErrInvalid
		}
		if ch.Kind == Object {
			if err := validate(ch, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// Root returns the validated patch root.
func (p Patch) Root() *Node { return p.root }

// TopKeys returns the patch's top-level keys.
func (p Patch) TopKeys() []string {
	ks := make([]string, 0, len(p.root.Kids))
	for k := range p.root.Kids {
		ks = append(ks, k)
	}
	return ks
}

// FlatLeaf is one (path, node) pair in byte-sorted path order.
type FlatLeaf struct {
	Path string
	Node *Node
}

// Flatten returns every leaf path (byte-sorted) with its node.
func Flatten(root *Node) []FlatLeaf {
	var out []FlatLeaf
	var walk func(n *Node, path string)
	walk = func(n *Node, path string) {
		if n == nil {
			return
		}
		if n.Kind != Object {
			out = append(out, FlatLeaf{Path: path, Node: n})
			return
		}
		for k, ch := range n.Kids {
			walk(ch, join(path, k))
		}
	}
	walk(root, "")
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// LeafPaths returns every leaf path (unsorted) under the subtree root.
func LeafPaths(root *Node) []string {
	fs := Flatten(root)
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path
	}
	return out
}

// Lookup follows dot-separated path under root and returns the node (nil if a
// segment is missing or an intermediate node is a leaf).
func Lookup(root *Node, path string) *Node {
	if root == nil {
		return nil
	}
	cur := root
	for path != "" {
		i := indexByte(path, '.')
		var seg, rest string
		if i < 0 {
			seg, rest = path, ""
		} else {
			seg, rest = path[:i], path[i+1:]
		}
		if cur.Kind != Object {
			return nil
		}
		cur = cur.Kids[seg]
		if cur == nil {
			return nil
		}
		path = rest
	}
	return cur
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
