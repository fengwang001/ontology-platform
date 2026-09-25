// Package topk maintains the top-K board of a single group under a total
// order: score descending, ties broken by ItemID ascending. Below-line items
// are kept, fully sorted, so a removed board item can be refilled.
package topk

import "sort"

// Item is one scored member of a group.
type Item struct {
	ItemID string
	Score  int
}

// Less is the total order: higher score first; on tie, smaller ItemID first.
func Less(a, b Item) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.ItemID < b.ItemID
}

// Group is the top-K state of one group. top (the board, len <= k) and rest
// (below-line) are each sorted by Less, and every top item ranks before every
// rest item, so All() is the whole group in total order.
type Group struct {
	k    int
	top  []Item
	rest []Item
	// lastChecks counts the comparisons the most recent Add used to decide
	// whether the new item enters the board (i.e. against the board's last
	// item). Unexported on purpose: not part of any public interface.
	lastChecks int
}

// New returns an empty Group whose board holds at most k items.
func New(k int) *Group { return &Group{k: k} }

// insertSorted returns s with it inserted at its total-order position.
func insertSorted(s []Item, it Item) []Item {
	i := sort.Search(len(s), func(i int) bool { return Less(it, s[i]) })
	s = append(s, Item{})
	copy(s[i+1:], s[i:])
	s[i] = it
	return s
}

// Add inserts it. Duplicate detection is the caller's job. Board entry is
// decided by a single comparison against the board's current last item.
func (g *Group) Add(it Item) {
	g.lastChecks = 0
	if len(g.top) < g.k {
		g.top = insertSorted(g.top, it)
		return
	}
	g.lastChecks = 1
	if Less(it, g.top[g.k-1]) {
		last := g.top[g.k-1]
		g.top = insertSorted(g.top[:g.k-1:g.k-1], it)
		g.rest = insertSorted(g.rest, last)
	} else {
		g.rest = insertSorted(g.rest, it)
	}
}

// Remove deletes id. If it was on the board, the first below-line item (the
// unique rank-K survivor) is promoted; removing a below-line item leaves the
// board untouched.
func (g *Group) Remove(id string) {
	if i := indexOf(g.top, id); i >= 0 {
		g.top = append(g.top[:i], g.top[i+1:]...)
		if len(g.top) < g.k && len(g.rest) > 0 {
			g.top = append(g.top, g.rest[0])
			g.rest = g.rest[1:]
		}
		return
	}
	if i := indexOf(g.rest, id); i >= 0 {
		g.rest = append(g.rest[:i], g.rest[i+1:]...)
	}
}

func indexOf(s []Item, id string) int {
	for i, it := range s {
		if it.ItemID == id {
			return i
		}
	}
	return -1
}

// Top returns a copy of the board, in total order.
func (g *Group) Top() []Item { return append([]Item(nil), g.top...) }

// All returns every surviving item in total order: board ++ below-line.
func (g *Group) All() []Item { return append(g.Top(), g.rest...) }
