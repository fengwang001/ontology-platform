package rtree

import (
	"sort"
	"strconv"
	"strings"
)

func (t *RTree) searchLocked(rect Rect) []int64 {
	visited := 0
	var ids []int64
	var dfs func(n *node)
	dfs = func(n *node) {
		visited++
		if n.height == 0 {
			for i := range n.entries {
				if intersects(n.entries[i].rect, rect) {
					ids = append(ids, n.entries[i].id)
				}
			}
			return
		}
		for i := range n.entries {
			if intersects(n.entries[i].rect, rect) {
				dfs(n.entries[i].child)
			}
		}
	}
	dfs(t.root)
	t.visited = visited
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

func dumpNode(n *node, b *strings.Builder) {
	if len(n.entries) == 0 {
		b.WriteString("L[]()")
		return
	}
	r := n.mbr()
	coords := strconv.FormatInt(r.X1, 10) + " " +
		strconv.FormatInt(r.Y1, 10) + " " +
		strconv.FormatInt(r.X2, 10) + " " +
		strconv.FormatInt(r.Y2, 10)
	if n.height == 0 {
		b.WriteString("L[" + coords + "](")
		for i := range n.entries {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(strconv.FormatInt(n.entries[i].id, 10))
		}
		b.WriteString(")")
		return
	}
	b.WriteString("N[" + coords + "]{")
	for i := range n.entries {
		if i > 0 {
			b.WriteByte(',')
		}
		dumpNode(n.entries[i].child, b)
	}
	b.WriteString("}")
}

func (t *RTree) dumpLocked() string {
	var b strings.Builder
	dumpNode(t.root, &b)
	return b.String()
}
