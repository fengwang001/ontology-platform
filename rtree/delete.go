package rtree

type reinsertItem struct {
	e      entry
	height int // height of the node the entry must be appended to
}

// locate walks depth-first in entry order and only enters nodes whose
// MBR contains rect. It returns the holding leaf and the root-to-leaf path.
func (t *RTree) locate(rect Rect, id int64) (*node, []*node) {
	entered := 0
	var found *node
	var fpath []*node
	var dfs func(n *node, path []*node) bool
	dfs = func(n *node, path []*node) bool {
		entered++
		path = append(path, n)
		if n.height == 0 {
			for i := range n.entries {
				if n.entries[i].id == id {
					found = n
					fpath = append(fpath[:0], path...)
					return true
				}
			}
			return false
		}
		for i := range n.entries {
			if contains(n.entries[i].rect, rect) {
				if dfs(n.entries[i].child, path) {
					return true
				}
			}
		}
		return false
	}
	dfs(t.root, nil)
	t.located = entered
	return found, fpath
}

func (t *RTree) deleteLocked(id int64) DeleteResult {
	rect := t.objects[id]
	leaf, path := t.locate(rect, id)

	idx := -1
	for i := range leaf.entries {
		if leaf.entries[i].id == id {
			idx = i
			break
		}
	}
	leaf.entries = append(leaf.entries[:idx], leaf.entries[idx+1:]...)
	delete(t.objects, id)
	t.count--

	var queue []reinsertItem
	removed := 0
	for i := len(path) - 1; i > 0; i-- {
		n := path[i]
		parent := path[i-1]
		if len(n.entries) < t.m {
			j := childIndex(parent, n)
			parent.entries = append(parent.entries[:j], parent.entries[j+1:]...)
			removed++
			for _, e := range n.entries {
				queue = append(queue, reinsertItem{e: e, height: n.height})
			}
		} else {
			j := childIndex(parent, n)
			r := n.mbr()
			parent.entries[j].rect = r
		}
	}

	if t.root.height > 0 && len(t.root.entries) == 0 {
		t.root = &node{height: 0}
	}

	reinserted := len(queue)
	for _, item := range queue {
		t.insertAtLevel(item.e, item.height)
	}

	for t.root.height > 0 && len(t.root.entries) == 1 {
		t.root = t.root.entries[0].child
	}

	return DeleteResult{Removed: removed, Reinserted: reinserted}
}
