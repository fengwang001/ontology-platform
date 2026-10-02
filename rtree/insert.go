package rtree

import "sort"

// chooseChild selects the entry whose child needs the least area
// enlargement; ties break by smaller child MBR area, then smaller index.
func chooseChild(n *node, r Rect) int {
	best := 0
	bestEnlarge := rectArea(unionRect(n.entries[0].rect, r)) - rectArea(n.entries[0].rect)
	bestArea := rectArea(n.entries[0].rect)
	for i := 1; i < len(n.entries); i++ {
		er := rectArea(unionRect(n.entries[i].rect, r)) - rectArea(n.entries[i].rect)
		ar := rectArea(n.entries[i].rect)
		if er < bestEnlarge || (er == bestEnlarge && ar < bestArea) ||
			(er == bestEnlarge && ar == bestArea && i < best) {
			best = i
			bestEnlarge = er
			bestArea = ar
		}
	}
	return best
}

// insertAtLevel descends to a node of the given height following the
// subtree-selection rule, appends e there and handles overflow splits
// bottom-up. It returns the number of splits performed.
func (t *RTree) insertAtLevel(e entry, height int) int {
	path := make([]*node, 0, 8)
	cur := t.root
	path = append(path, cur)
	for cur.height > height {
		j := chooseChild(cur, e.rect)
		cur = cur.entries[j].child
		path = append(path, cur)
	}
	cur.entries = append(cur.entries, e)

	splits := 0
	for i := len(path) - 1; i >= 0; i-- {
		n := path[i]
		if len(n.entries) > t.M {
			sort.SliceStable(n.entries, func(a, b int) bool {
				ea := n.entries[a].rect
				eb := n.entries[b].rect
				sa := [2]int64{ea.X1 + ea.X2, ea.Y1 + ea.Y2}
				sb := [2]int64{eb.X1 + eb.X2, eb.Y1 + eb.Y2}
				return sa[0] < sb[0] || (sa[0] == sb[0] && sa[1] < sb[1])
			})
			k := (t.M + 2) / 2 // ceil((M+1)/2)
			moved := append([]entry(nil), n.entries[k:]...)
			n.entries = n.entries[:k]
			right := &node{height: n.height, entries: moved}
			splits++
			if i == 0 {
				t.root = &node{
					height: n.height + 1,
					entries: []entry{
						{rect: n.mbr(), child: n},
						{rect: right.mbr(), child: right},
					},
				}
				return splits
			}
			parent := path[i-1]
			j := childIndex(parent, n)
			parent.entries[j].rect = n.mbr()
			rightEntry := entry{rect: right.mbr(), child: right}
			parent.entries = append(parent.entries, entry{})
			copy(parent.entries[j+2:], parent.entries[j+1:])
			parent.entries[j+1] = rightEntry
		} else if i > 0 {
			parent := path[i-1]
			j := childIndex(parent, n)
			parent.entries[j].rect = n.mbr()
		}
	}
	return splits
}

func childIndex(parent *node, child *node) int {
	for i := range parent.entries {
		if parent.entries[i].child == child {
			return i
		}
	}
	return -1
}
