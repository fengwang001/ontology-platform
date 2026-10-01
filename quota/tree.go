package quota

// ID is the numeric node identifier. Root is 0; every successful Mkdir or
// AddFile receives the next consecutive id starting from 1.
type ID int64

type node struct {
	id      ID
	parent  ID
	alive   bool
	isDir   bool
	size    int64 // files only
	reserve int64 // directories only: r_d

	// Directories only; -1 means unlimited.
	bytesQuota   int64
	entriesQuota int64

	// Aggregated subtree counters for directories:
	// bytes   = sum of file sizes in the subtree
	// entries = number of files and directories strictly inside
	bytes   int64
	entries int64
	// resAgg is the sum of r over all directories in this subtree
	// (including this directory); R(d) for directories.
	resAgg int64
}

type tree struct {
	nodes    []*node
	children map[ID][]ID
}

func newTree() *tree {
	root := &node{
		id:           0,
		parent:       0,
		alive:        true,
		isDir:        true,
		bytesQuota:   -1,
		entriesQuota: -1,
	}
	return &tree{nodes: []*node{root}, children: map[ID][]ID{0: {}}}
}

func (t *tree) get(id ID) (*node, bool) {
	if id < 0 || int(id) >= len(t.nodes) {
		return nil, false
	}
	n := t.nodes[id]
	return n, n.alive
}

func (t *tree) allocID() ID {
	return ID(len(t.nodes))
}

func (t *tree) clone() *tree {
	cp := &tree{nodes: make([]*node, len(t.nodes))}
	for i, n := range t.nodes {
		c := *n
		cp.nodes[i] = &c
	}
	cp.children = make(map[ID][]ID, len(t.children))
	for k, v := range t.children {
		cp.children[k] = append([]ID(nil), v...)
	}
	return cp
}

// parentChain returns the directory itself and all its ancestors, nearest
// first (d, parent(d), ..., root).
func (t *tree) parentChain(d ID) []ID {
	chain := make([]ID, 0, 8)
	for {
		chain = append(chain, d)
		n := t.nodes[d]
		if d == 0 {
			return chain
		}
		d = n.parent
	}
}
