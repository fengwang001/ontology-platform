// Package bptree implements a streaming bulk loader for a read-only B+ tree.
//
// Keys arrive on a strictly increasing stream. Leaves are sealed
// bottom-up with a configurable fill percentage, and on Finish the last two
// pages of every level are rebalanced so that the resulting tree is
// deterministic for any input.
package bptree

import (
	"sort"
	"sync"
)

// Page is an immutable snapshot of one tree page.
type Page struct {
	// Keys stores leaf keys for a leaf page, or separator keys for an
	// internal page. An internal page with n children holds n-1 separators.
	Keys []string

	// Children holds the level-local indices of child pages for an
	// internal page. It is nil for a leaf page.
	Children []int

	Leaf bool
}

type node struct {
	keys     []string
	children []*node
	leaf     bool
	minKey   string
}

// Loader is a streaming B+ tree bulk loader.
//
// A Loader is safe for concurrent calls to Add (including AddMany),
// Finish and Get after Finish; the result is equivalent to some serial
// interleaving of the calls.
type Loader struct {
	mu sync.Mutex

	C  int
	B  int
	p  int
	mL int
	mI int
	tL int
	tI int

	finished bool

	// Streaming leaf state: sealed leaves plus the open page, which is
	// only created when the first key after a seal arrives.
	leaves []*node
	open   []string

	lastKey string
	hasLast bool

	// Finished tree, bottom (leaves) to top (root).
	levels [][]*node
	root   *node
	height int
}

// New constructs a Loader with leaf capacity C, internal fanout B and fill
// percentage p (1..100).
func New(C, B, p int) (*Loader, error) {
	if C < 2 {
		return nil, ErrBadLeafCapacity
	}
	if B < 3 {
		return nil, ErrBadInternalFanout
	}
	if p < 1 || p > 100 {
		return nil, ErrBadPercentage
	}
	mL := C / 2
	mI := (B + 1) / 2
	tL := (C*p + 99) / 100
	if tL < mL {
		tL = mL
	}
	tI := (B*p + 99) / 100
	if tI < mI {
		tI = mI
	}
	return &Loader{
		C: C, B: B, p: p,
		mL: mL, mI: mI, tL: tL, tI: tI,
	}, nil
}

// Add appends a strictly increasing key to the stream.
func (l *Loader) Add(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.finished {
		return ErrAlreadyFinished
	}
	if key == "" {
		return ErrEmptyKey
	}
	if l.hasLast && key <= l.lastKey {
		return &NonMonotonicError{
			Key:      key,
			Index:    l.acceptedCount(),
			Previous: l.lastKey,
		}
	}
	l.appendKey(key)
	return nil
}

// AddMany appends a batch of strictly increasing keys. The whole batch is
// validated before any state changes, so a rejected batch leaves the loader
// untouched.
func (l *Loader) AddMany(keys []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.finished {
		return ErrAlreadyFinished
	}

	prev := l.lastKey
	hasPrev := l.hasLast
	for i, key := range keys {
		if key == "" {
			return ErrEmptyKey
		}
		if hasPrev && key <= prev {
			return &NonMonotonicError{
				Key:      key,
				Index:    l.acceptedCount() + i,
				Previous: prev,
			}
		}
		prev = key
		hasPrev = true
	}

	for _, key := range keys {
		l.appendKey(key)
	}
	return nil
}

// Finish freezes the stream, rebalances the final pages and builds all
// internal levels.
func (l *Loader) Finish() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.finished {
		return ErrAlreadyFinished
	}
	l.finished = true

	if len(l.leaves) == 0 && len(l.open) == 0 {
		empty := &node{leaf: true}
		l.root = empty
		l.levels = [][]*node{{empty}}
		l.height = 1
		return nil
	}

	level := make([]*node, 0, len(l.leaves)+1)
	level = append(level, l.leaves...)
	if len(l.open) > 0 {
		level = append(level, newLeaf(l.open))
	}

	level = rebalanceLastTwoLeaves(level, l.C, l.mL)

	l.levels = [][]*node{level}

	for len(level) > 1 {
		parents := l.buildInternalLevel(level)
		l.levels = append(l.levels, parents)
		level = parents
	}

	l.root = level[0]
	l.height = len(l.levels)
	return nil
}

// Get reports whether key exists and the number of pages visited.
// Get must not be called before Finish.
func (l *Loader) Get(key string) (exists bool, pagesVisited int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.finished {
		return false, 0, ErrGetBeforeFinish
	}

	cur := l.root
	for {
		pagesVisited++
		if cur.leaf {
			i := sort.SearchStrings(cur.keys, key)
			return i < len(cur.keys) && cur.keys[i] == key, pagesVisited, nil
		}
		// k >= separator[j] goes right; locate the first separator > k.
		idx := sort.Search(len(cur.keys), func(i int) bool {
			return cur.keys[i] > key
		})
		cur = cur.children[idx]
	}
}

// TreeHeight returns the height of the finished tree, in pages (root leaf == 1).
func (l *Loader) TreeHeight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.height
}

// RootIndex returns the level-local index of the root page.
func (l *Loader) RootIndex() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.levels[len(l.levels)-1]) - 1
}

// Levels returns a copy of the finished tree, bottom (leaves) to top (root).
func (l *Loader) Levels() [][]Page {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.levels == nil {
		return nil
	}
	out := make([][]Page, len(l.levels))
	for li, nodes := range l.levels {
		var childIndex map[*node]int
		if li > 0 {
			childIndex = make(map[*node]int, len(l.levels[li-1]))
			for i, n := range l.levels[li-1] {
				childIndex[n] = i
			}
		}
		pages := make([]Page, len(nodes))
		for i, n := range nodes {
			pages[i] = Page{
				Keys: append([]string(nil), n.keys...),
				Leaf: n.leaf,
			}
			if !n.leaf {
				children := make([]int, len(n.children))
				for ci, ch := range n.children {
					children[ci] = childIndex[ch]
				}
				pages[i].Children = children
			}
		}
		out[li] = pages
	}
	return out
}

func newLeaf(keys []string) *node {
	n := &node{leaf: true, keys: append([]string(nil), keys...)}
	if len(keys) > 0 {
		n.minKey = keys[0]
	}
	return n
}

func (l *Loader) acceptedCount() int {
	n := len(l.open)
	for _, leaf := range l.leaves {
		n += len(leaf.keys)
	}
	return n
}

// appendKey must be called with l.mu held and the key already validated.
func (l *Loader) appendKey(key string) {
	if l.open == nil {
		l.open = make([]string, 0, l.tL)
	}
	l.open = append(l.open, key)
	l.lastKey = key
	l.hasLast = true
	if len(l.open) == l.tL {
		l.leaves = append(l.leaves, newLeaf(l.open))
		l.open = nil
	}
}

// buildInternalLevel groups adjacent children into pages of at most tI
// children, rebalancing the last two groups when the final one has fewer
// than mI children.
func (l *Loader) buildInternalLevel(children []*node) []*node {
	groups := groupIndices(len(children), l.tI, l.mI, l.B)
	parents := make([]*node, 0, len(groups))
	for _, g := range groups {
		kids := children[g[0]:g[1]]
		p := &node{
			leaf:     false,
			children: append([]*node(nil), kids...),
			minKey:   kids[0].minKey,
		}
		p.keys = make([]string, len(kids)-1)
		for j := 1; j < len(kids); j++ {
			// Separator j is the minimum key of subtree j+1.
			p.keys[j-1] = kids[j].minKey
		}
		parents = append(parents, p)
	}
	return parents
}

// groupIndices partitions [0,n) into adjacent ranges of size t, except the
// final range. If the final range has fewer than min elements it is merged
// with the previous range; the merged range stays one page when its size is
// at most cap, otherwise it is split evenly with the earlier page taking
// ceil(sum/2) and the later page floor(sum/2).
func groupIndices(n, t, min, cap int) [][2]int {
	var groups [][2]int
	for i := 0; i < n; {
		j := i + t
		if j > n {
			j = n
		}
		groups = append(groups, [2]int{i, j})
		i = j
	}
	if len(groups) >= 2 {
		last := groups[len(groups)-1]
		if last[1]-last[0] < min {
			prev := groups[len(groups)-2]
			sum := last[1] - prev[0]
			if sum <= cap {
				groups[len(groups)-2] = [2]int{prev[0], last[1]}
				groups = groups[:len(groups)-1]
			} else {
				mid := prev[0] + (sum+1)/2
				groups[len(groups)-2] = [2]int{prev[0], mid}
				groups[len(groups)-1] = [2]int{mid, last[1]}
			}
		}
	}
	return groups
}

// rebalanceLastTwoLeaves rebalances the final two leaf pages.
func rebalanceLastTwoLeaves(level []*node, C, mL int) []*node {
	if len(level) < 2 {
		return level
	}
	last := level[len(level)-1]
	if len(last.keys) >= mL {
		return level
	}
	prev := level[len(level)-2]
	joined := make([]string, 0, len(prev.keys)+len(last.keys))
	joined = append(joined, prev.keys...)
	joined = append(joined, last.keys...)

	switch {
	case len(joined) <= C:
		level[len(level)-2] = newLeaf(joined)
		level = level[:len(level)-1]
	default:
		mid := (len(joined) + 1) / 2
		level[len(level)-2] = newLeaf(joined[:mid])
		level[len(level)-1] = newLeaf(joined[mid:])
	}
	return level
}
