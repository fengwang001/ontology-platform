// Package check holds a naive reference model for the indexed heap.
package check

import (
	"sort"

	"ontology/item"
)

// Naive is a deliberately slow priority queue: after every mutation it
// fully re-sorts its active elements. It serves as an oracle in tests.
type Naive struct {
	cur map[int]item.Item
	ord []int
	seq int
}

func NewNaive() *Naive { return &Naive{cur: map[int]item.Item{}} }

func (n *Naive) Push(it item.Item) int {
	id := n.seq
	n.seq++
	n.ord = append(n.ord, id)
	n.cur[id] = it
	n.resort()
	return id
}

func (n *Naive) Pop() (int, item.Item, bool) {
	if len(n.ord) == 0 {
		return 0, item.Item{}, false
	}
	id := n.ord[0]
	it := n.cur[id]
	delete(n.cur, id)
	rest := make([]int, len(n.ord)-1)
	copy(rest, n.ord[1:])
	n.ord = rest
	return id, it, true
}

func (n *Naive) DecreaseKey(id int, it item.Item) (rejected bool) {
	cur, ok := n.cur[id]
	if !ok || !item.Less(it, cur) {
		return true
	}
	n.cur[id] = it
	n.resort()
	return false
}

func (n *Naive) Len() int { return len(n.ord) }

func (n *Naive) resort() {
	sort.Slice(n.ord, func(i, j int) bool {
		return item.Less(n.cur[n.ord[i]], n.cur[n.ord[j]])
	})
}
