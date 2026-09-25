// Package trie implements a byte-level prefix tree with reference-counted
// pruning. It is not goroutine-safe; callers must synchronize.
package trie

type node struct {
	kids map[byte]*node
	term bool // terminal marker: a string ends here
	freq int  // accumulated frequency, meaningful only when term
	refs int  // number of terminal strings in this subtree
}

// Trie is a prefix tree over byte strings.
type Trie struct {
	root    *node
	n       int // distinct strings
	visited int // nodes touched by the last Insert/Delete (test-only counter)
}

// New returns an empty Trie.
func New() *Trie { return &Trie{root: &node{}} }

// Count returns the number of distinct strings.
func (t *Trie) Count() int { return t.n }

// Insert adds s with frequency f; an existing s accumulates f.
func (t *Trie) Insert(s string, f int) {
	path := make([]*node, 0, len(s)+1)
	cur := t.root
	path = append(path, cur)
	for i := 0; i < len(s); i++ {
		if cur.kids == nil {
			cur.kids = map[byte]*node{}
		}
		if cur.kids[s[i]] == nil {
			cur.kids[s[i]] = &node{}
		}
		cur = cur.kids[s[i]]
		path = append(path, cur)
	}
	t.visited += len(path)
	if !cur.term {
		for _, nd := range path {
			nd.refs++
		}
		cur.term = true
		t.n++
	}
	cur.freq += f
}

// Delete removes s entirely. It reports whether s existed. On a miss it
// changes nothing. After removal it decrements the reference count along
// the path and prunes every node whose count drops to zero.
func (t *Trie) Delete(s string) bool {
	path := make([]*node, 0, len(s)+1)
	cur := t.root
	path = append(path, cur)
	for i := 0; i < len(s); i++ {
		nxt := cur.kids[s[i]]
		if nxt == nil {
			t.visited += len(path)
			return false
		}
		cur = nxt
		path = append(path, cur)
	}
	t.visited += len(path)
	if !cur.term {
		return false
	}
	cur.term = false
	cur.freq = 0
	t.n--
	for i := len(path) - 1; i >= 0; i-- {
		nd := path[i]
		nd.refs--
		t.visited++
		if nd.refs == 0 && i > 0 {
			delete(path[i-1].kids, s[i-1])
		}
	}
	return true
}

// Walk calls fn for every terminal string under prefix, in lexicographic
// (byte) order. Only the prefix subtree is traversed.
func (t *Trie) Walk(prefix string, fn func(s string, freq int)) {
	cur := t.root
	for i := 0; i < len(prefix); i++ {
		cur = cur.kids[prefix[i]]
		if cur == nil {
			return
		}
	}
	var rec func(nd *node, buf []byte)
	rec = func(nd *node, buf []byte) {
		if nd.term {
			fn(string(buf), nd.freq)
		}
		for b := 0; b < 256; b++ {
			if kid := nd.kids[byte(b)]; kid != nil {
				rec(kid, append(buf, byte(b)))
			}
		}
	}
	rec(cur, []byte(prefix))
}

// CheckRefs verifies that every node's refs equals the number of terminal
// strings in its subtree, recomputed naively.
func (t *Trie) CheckRefs() bool {
	var rec func(nd *node) (int, bool)
	rec = func(nd *node) (int, bool) {
		sum := 0
		if nd.term {
			sum = 1
		}
		for _, kid := range nd.kids {
			c, ok := rec(kid)
			if !ok {
				return 0, false
			}
			sum += c
		}
		return sum, sum == nd.refs
	}
	_, ok := rec(t.root)
	return ok
}
