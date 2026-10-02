package rtree

func (t *RTree) deleteLocked(id int64, rect Rect) (int, int, int, int, bool) {
	path, located, found := locateObject(t.root, id, rect, 0)
	if !found {
		return 0, 0, 0, located, false
	}

	leafIndex := path[len(path)-1]
	leaf := t.nodeByPath(path[:len(path)-1])
	leaf.entries = append(leaf.entries[:leafIndex], leaf.entries[leafIndex+1:]...)

	var queue []entry
	removed := 0
	subtreeReinserted := 0
	for level := len(path) - 1; level >= 0; level-- {
		current := t.nodeByPath(path[:level])
		if level > 0 && len(current.entries) < t.min {
			queue = append(queue, current.entries...)
			parent := t.nodeByPath(path[:level-1])
			childIndex := path[level-1]
			parent.entries = append(parent.entries[:childIndex], parent.entries[childIndex+1:]...)
			removed++
			continue
		}
		current.rect = nodeBounds(current.entries)
		if level > 0 {
			parent := t.nodeByPath(path[:level-1])
			parent.entries[path[level-1]].rect = current.rect
		}
	}

	reinserted := len(queue)
	for _, item := range queue {
		if item.child != nil {
			subtreeReinserted++
		}
		t.insertLocked(item)
	}

	for t.root.height > 0 && len(t.root.entries) == 1 {
		t.root = t.root.entries[0].child
	}

	return removed, reinserted, subtreeReinserted, located, true
}

func locateObject(n *node, id int64, rect Rect, located int) ([]int, int, bool) {
	located++
	if n.height == 0 {
		for index, item := range n.entries {
			if item.id == id {
				return []int{index}, located, true
			}
		}
		return nil, located, false
	}

	for index, item := range n.entries {
		if !rectContains(item.rect, rect) {
			continue
		}
		childPath, nextLocated, found := locateObject(item.child, id, rect, located)
		located = nextLocated
		if found {
			return append([]int{index}, childPath...), located, true
		}
	}
	return nil, located, false
}

func (t *RTree) nodeByPath(path []int) *node {
	current := t.root
	for _, index := range path {
		current = current.entries[index].child
	}
	return current
}
