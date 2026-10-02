package rtree

import (
	"fmt"
	"sort"
)

// checkInvariants verifies height uniformity, entry-count bounds,
// tight MBRs and single occurrence of every object.
func checkInvariants(t *RTree) error {
	seen := map[int64]int{}
	var check func(n *node, isRoot bool, expHeight int) error
	check = func(n *node, isRoot bool, expHeight int) error {
		if n.height != expHeight {
			return fmt.Errorf("unexpected height %d want %d", n.height, expHeight)
		}
		if len(n.entries) > t.M {
			return fmt.Errorf("node has %d > M=%d entries", len(n.entries), t.M)
		}
		if isRoot {
			if n.height > 0 && (len(n.entries) < 2) {
				return fmt.Errorf("internal root has %d entries", len(n.entries))
			}
		} else if len(n.entries) < t.m {
			return fmt.Errorf("non-root node has %d < m=%d", len(n.entries), t.m)
		}
		if n.height == 0 {
			for _, e := range n.entries {
				if e.child != nil {
					return fmt.Errorf("leaf entry %d has child", e.id)
				}
				if r, ok := t.objects[e.id]; !ok || r != e.rect {
					return fmt.Errorf("leaf id %d not matching object map", e.id)
				}
				seen[e.id]++
			}
			if len(n.entries) > 0 {
				r := n.mbr()
				for _, e := range n.entries {
					if !contains(r, e.rect) {
						return fmt.Errorf("leaf MBR %v misses entry %v", r, e.rect)
					}
				}
			}
			return nil
		}
		for _, e := range n.entries {
			if e.child == nil {
				return fmt.Errorf("internal entry lacks child")
			}
			if err := check(e.child, false, n.height-1); err != nil {
				return err
			}
		}
		r := n.mbr()
		for _, e := range n.entries {
			if e.rect != e.child.mbr() {
				return fmt.Errorf("stale child MBR %v vs %v", e.rect, e.child.mbr())
			}
			if !contains(r, e.rect) {
				return fmt.Errorf("internal MBR %v misses child %v", r, e.rect)
			}
		}
		return nil
	}
	if err := check(t.root, true, t.root.height); err != nil {
		return err
	}
	if len(seen) != t.count {
		return fmt.Errorf("seen %d distinct objects but count=%d", len(seen), t.count)
	}
	if len(seen) != len(t.objects) {
		return fmt.Errorf("seen %d but map %d", len(seen), len(t.objects))
	}
	for id, c := range seen {
		if c != 1 {
			return fmt.Errorf("id %d appears %d times", id, c)
		}
	}
	return nil
}

func bruteSearch(objs map[int64]Rect, q Rect) []int64 {
	var ids []int64
	for id, r := range objs {
		if intersects(r, q) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}
