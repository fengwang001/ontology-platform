package tree

import "errors"

var (
	// ErrIllegalTree covers every static configuration violation.
	ErrIllegalTree       = errors.New("tree: illegal configuration")
	ErrRootMinNotCap     = errors.New("tree: root Min must equal C")
	ErrMinOverMax        = errors.New("tree: Min > Max")
	ErrChildMinOverflows = errors.New("tree: sum of child Min > parent Min")
	ErrNoRoot            = errors.New("tree: missing or duplicated root")
	ErrBadParent         = errors.New("tree: node references unknown parent")
)

// Node is one queue node in the tree (root -> department -> leaf).
type Node struct {
	ID       string
	Min, Max int64
	Parent   string // empty for the root
}

// Tree is a validated, immutable queue tree of total capacity C.
type Tree struct {
	C      int64
	nodes  map[string]*Node
	kids   map[string][]string
	leaves map[string]bool
	root   string
}

// Build validates: unique IDs, single root with Min==C, Min<=Max,
// sum(child Min) <= parent Min. Capacity bounds are checked per node.
func Build(C int64, nodes []Node) (*Tree, error) {
	t := &Tree{C: C, nodes: map[string]*Node{}, kids: map[string][]string{}, leaves: map[string]bool{}}
	roots := 0
	for _, n := range nodes {
		if n.ID == "" {
			return nil, ErrIllegalTree
		}
		if _, dup := t.nodes[n.ID]; dup {
			return nil, ErrIllegalTree
		}
		cp := n
		t.nodes[n.ID] = &cp
		if n.Parent == "" {
			roots++
			t.root = n.ID
		}
		if n.Min < 0 || n.Max < 0 || n.Min > n.Max {
			return nil, ErrMinOverMax
		}
	}
	if roots != 1 {
		return nil, ErrNoRoot
	}
	if t.nodes[t.root].Min != C || t.nodes[t.root].Max != C {
		return nil, ErrRootMinNotCap
	}
	for _, n := range t.nodes {
		if n.ID == t.root {
			continue
		}
		p, ok := t.nodes[n.Parent]
		if !ok {
			return nil, ErrBadParent
		}
		t.kids[p.ID] = append(t.kids[p.ID], n.ID)
	}
	for id, ks := range t.kids {
		var sum int64
		for _, k := range ks {
			sum += t.nodes[k].Min
			if len(t.kids[k]) == 0 {
				t.leaves[k] = true
			}
		}
		if sum > t.nodes[id].Min {
			return nil, ErrChildMinOverflows
		}
	}
	if len(t.kids[t.root]) == 0 {
		t.leaves[t.root] = true
	}
	return t, nil
}

func (t *Tree) Node(id string) (Node, bool) {
	n, ok := t.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

func (t *Tree) Root() string { return t.root }
func (t *Tree) Leaves() []string {
	out := make([]string, 0, len(t.leaves))
	for id := range t.leaves {
		out = append(out, id)
	}
	return out
}

func (t *Tree) Children(id string) []string {
	out := append([]string(nil), t.kids[id]...)
	return out
}

func (t *Tree) IsLeaf(id string) bool { return t.leaves[id] }

// Siblings reports whether a and b share the same parent.
func (t *Tree) Siblings(a, b string) bool {
	na, oa := t.nodes[a]
	nb, ob := t.nodes[b]
	return oa && ob && na.Parent == nb.Parent
}
