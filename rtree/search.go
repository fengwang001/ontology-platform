package rtree

import "sort"

func (t *RTree) Search(rect Rect) ([]int64, error) {
	if !validRect(rect) {
		return nil, ErrInvalidArgument
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var ids []int64
	visited := searchNode(t.root, rect, &ids, true)
	t.visited.Store(int64(visited))
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func searchNode(n *node, query Rect, ids *[]int64, isRoot bool) int {
	if len(n.entries) == 0 {
		return 1
	}
	visited := 1
	if !rectsIntersect(n.rect, query) {
		if isRoot {
			return 1
		}
		return 0
	}
	if n.height == 0 {
		for _, item := range n.entries {
			if rectsIntersect(item.rect, query) {
				*ids = append(*ids, item.id)
			}
		}
		return visited
	}

	for _, item := range n.entries {
		visited += searchNode(item.child, query, ids, false)
	}
	return visited
}
