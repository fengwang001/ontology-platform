package streamtree

import (
	"fmt"
	"sort"
)

func (a *Allocator) alive(id int) bool {
	return id == RootID || a.streams[id] != nil
}

func (a *Allocator) attach(id, parent int, exclusive bool) {
	if exclusive {
		for _, child := range sortedKeys(a.children[parent]) {
			a.moveSubtree(child, id)
		}
	}
	a.children[parent][id] = struct{}{}
	a.streams[id].parent = parent
}

func (a *Allocator) detach(id int) {
	parent := a.streams[id].parent
	delete(a.children[parent], id)
}

func (a *Allocator) moveSubtree(id, newParent int) {
	oldParent := a.streams[id].parent
	delete(a.children[oldParent], id)
	a.children[newParent][id] = struct{}{}
	a.streams[id].parent = newParent
}

func (a *Allocator) isDescendant(ancestor, candidate int) bool {
	for current := candidate; current != RootID; current = a.streams[current].parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

func sortedKeys(set map[int]struct{}) []int {
	keys := make([]int, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func (a *Allocator) log(operation string, id, parent, weight int, exclusive bool, result map[int]int, reason string) {
	if a.w == nil {
		return
	}

	a.logMu.Lock()
	defer a.logMu.Unlock()

	var suffix string
	if result != nil {
		suffix = fmt.Sprintf(" output=%v", result)
	}
	fmt.Fprintf(a.w, "op=%s id=%d parent=%d weight=%d exclusive=%t reason=%q%s\n",
		operation, id, parent, weight, exclusive, reason, suffix)
}

func (a *Allocator) reject(operation string, id, parent, weight int, exclusive bool, err error) error {
	a.log(operation, id, parent, weight, exclusive, nil, "rejected: "+err.Error())
	return err
}
