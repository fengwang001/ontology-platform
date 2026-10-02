package rtree

import (
	"fmt"
	"sort"
	"testing"
)

func assertInvariants(t *testing.T, tree *RTree, expected map[int64]Rect) {
	t.Helper()
	if len(tree.objects) != len(expected) {
		t.Fatalf("objects = %d, want %d", len(tree.objects), len(expected))
	}
	seen := make(map[int64]bool)
	height := checkNode(t, tree, tree.root, true, seen, expected)
	if tree.root.height != height {
		t.Fatalf("root height = %d, actual %d", tree.root.height, height)
	}
	if len(seen) != len(expected) {
		t.Fatalf("tree objects = %d, expected %d", len(seen), len(expected))
	}
	for id, rect := range expected {
		if !seen[id] {
			t.Fatalf("id %d missing", id)
		}
		if tree.objects[id] != rect {
			t.Fatalf("stored rect %v for id %d, want %v", tree.objects[id], id, rect)
		}
	}
}

func checkNode(t *testing.T, tree *RTree, n *node, isRoot bool, seen map[int64]bool, expected map[int64]Rect) int {
	t.Helper()
	if len(n.entries) == 0 {
		if n.height != 0 {
			t.Fatalf("empty node height = %d", n.height)
		}
		return 0
	}

	if len(n.entries) > tree.max {
		t.Fatalf("node has %d entries > max %d", len(n.entries), tree.max)
	}
	if !isRoot && len(n.entries) < tree.min {
		t.Fatalf("non-root node has %d entries < min %d", len(n.entries), tree.min)
	}

	actualBounds := nodeBounds(n.entries)
	if n.rect != actualBounds {
		t.Fatalf("stored bounds %v, tight bounds %v", n.rect, actualBounds)
	}

	if n.height == 0 {
		for _, item := range n.entries {
			if item.child != nil || item.height != 0 {
				t.Fatalf("leaf entry %d has child data", item.id)
			}
			if item.rect != expected[item.id] {
				t.Fatalf("leaf id %d rect %v, want %v", item.id, item.rect, expected[item.id])
			}
			if seen[item.id] {
				t.Fatalf("id %d appears more than once", item.id)
			}
			seen[item.id] = true
		}
		return 0
	}

	if isRoot && len(n.entries) < 2 {
		t.Fatalf("internal root has %d entries", len(n.entries))
	}
	for _, item := range n.entries {
		if item.child == nil || item.height != item.child.height || item.child.height != n.height-1 || item.rect != item.child.rect {
			t.Fatalf("internal entry for child is inconsistent")
		}
		childHeight := checkNode(t, tree, item.child, false, seen, expected)
		if childHeight != n.height-1 {
			t.Fatalf("uneven subtree heights: parent %d child %d", n.height, childHeight)
		}
	}
	return n.height
}

func bruteForce(expected map[int64]Rect, query Rect) []int64 {
	var ids []int64
	for id, rect := range expected {
		if rectsIntersect(rect, query) {
			ids = append(ids, id)
		}
	}
	sortIDs(ids)
	return ids
}

func sameIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func dumpInvariantFailure(tree *RTree, step int, op string, got interface{}, want interface{}) string {
	return fmt.Sprintf("step=%d op=%s dump=%s got=%v want=%v", step, op, tree.Dump(), got, want)
}

func sortIDs(ids []int64) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
