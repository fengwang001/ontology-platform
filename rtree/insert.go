package rtree

import "sort"

func (t *RTree) insertLocked(newEntry entry) int {
	siblingEntry, rootSplit, totalSplits := t.insertAt(t.root, newEntry)
	if rootSplit {
		oldRoot := t.root
		siblingEntry.height = oldRoot.height
		oldEntry := entry{rect: oldRoot.rect, child: oldRoot, height: oldRoot.height}
		t.root = &node{
			height:  oldRoot.height + 1,
			entries: []entry{oldEntry, siblingEntry},
		}
		t.root.rect = nodeBounds(t.root.entries)
		return totalSplits + 1
	}
	return 0
}

func (t *RTree) insertAt(n *node, newEntry entry) (entry, bool, int) {
	totalSplits := 0
	targetHeight := 0
	if newEntry.child != nil {
		targetHeight = newEntry.child.height + 1
	}
	if len(n.entries) == 0 && n.height > targetHeight {
		n.height = targetHeight
	}
	if n.height == targetHeight {
		n.entries = append(n.entries, newEntry)
	} else {
		index := chooseChild(n, newEntry.rect)
		originalChild := n.entries[index].child
		siblingEntry, childSplit, childTotalSplits := t.insertAt(n.entries[index].child, newEntry)
		totalSplits += childTotalSplits
		n.entries[index] = entry{
			rect:   originalChild.rect,
			child:  originalChild,
			height: originalChild.height,
		}
		if childSplit {
			n.entries = append(n.entries, entry{})
			copy(n.entries[index+2:], n.entries[index+1:])
			n.entries[index+1] = entry{
				rect:   siblingEntry.child.rect,
				child:  siblingEntry.child,
				height: siblingEntry.child.height,
			}
		}
	}

	n.rect = nodeBounds(n.entries)
	if len(n.entries) <= t.max {
		return entry{}, false, totalSplits
	}

	sibling := t.splitNode(n)
	return entry{rect: sibling.rect, child: sibling, height: sibling.height}, true, totalSplits + 1
}

func chooseChild(parent *node, rect Rect) int {
	best := 0
	bestExpansion := area(unionRect(parent.entries[0].rect, rect)) - area(parent.entries[0].rect)
	bestArea := area(parent.entries[0].rect)

	for index := 1; index < len(parent.entries); index++ {
		childRect := parent.entries[index].rect
		expansion := area(unionRect(childRect, rect)) - area(childRect)
		childArea := area(childRect)
		if expansion < bestExpansion ||
			(expansion == bestExpansion && childArea < bestArea) ||
			(expansion == bestExpansion && childArea == bestArea && index < best) {
			best = index
			bestExpansion = expansion
			bestArea = childArea
		}
	}
	return best
}

func (t *RTree) splitNode(n *node) *node {
	entries := make([]entry, len(n.entries))
	copy(entries, n.entries)
	oldHeight := n.height
	sort.SliceStable(entries, func(i, j int) bool {
		left := entries[i].rect
		right := entries[j].rect
		leftX := left.X1 + left.X2
		leftY := left.Y1 + left.Y2
		rightX := right.X1 + right.X2
		rightY := right.Y1 + right.Y2
		return leftX < rightX || (leftX == rightX && leftY < rightY)
	})

	firstCount := (t.max + 2) / 2
	if oldHeight > 0 {
		return t.splitInternalNode(n, entries, firstCount)
	}

	leftEntries := make([]entry, firstCount)
	copy(leftEntries, entries[:firstCount])
	n.entries = leftEntries
	n.rect = nodeBounds(n.entries)
	rightEntries := make([]entry, len(entries)-firstCount)
	copy(rightEntries, entries[firstCount:])
	sibling := &node{
		height:  oldHeight,
		entries: rightEntries,
	}
	sibling.rect = nodeBounds(sibling.entries)
	return sibling
}

func (t *RTree) splitInternalNode(n *node, entries []entry, firstCount int) *node {
	leftEntries := make([]entry, firstCount)
	rightEntries := make([]entry, len(entries)-firstCount)

	for index, item := range entries[:firstCount] {
		leftEntries[index] = entry{rect: item.child.rect, child: item.child, height: item.child.height}
	}
	for index, item := range entries[firstCount:] {
		rightEntries[index] = entry{rect: item.child.rect, child: item.child, height: item.child.height}
	}

	n.entries = leftEntries
	n.rect = nodeBounds(n.entries)
	sibling := &node{height: n.height, entries: rightEntries}
	sibling.rect = nodeBounds(sibling.entries)
	return sibling
}
