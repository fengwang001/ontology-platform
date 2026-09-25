// Package trie builds the Aho-Corasick trie: insertion of patterns,
// failure links, and flattened output links. It depends on nothing.
package trie

// Node is one trie node. ch is the child edges, fail the failure link,
// term the pattern indices ending exactly here, out term plus every
// terminal pattern along the fail chain, in report order (self first).
type Node struct {
	ch   map[byte]*Node
	fail *Node
	term []int
	out  []int
}

// Trie is the pattern automaton skeleton. Build must be called after
// all Insert calls, before the trie is handed to the matcher.
type Trie struct{ root *Node }

// New returns an empty trie with a root.
func New() *Trie { return &Trie{root: &Node{}} }

// Root returns the root node; its fail link is itself after Build.
func (t *Trie) Root() *Node { return t.root }

// Insert adds pattern pat under index idx; the path end becomes terminal.
func (t *Trie) Insert(pat string, idx int) {
	n := t.root
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		if n.ch == nil {
			n.ch = map[byte]*Node{}
		}
		nx, ok := n.ch[c]
		if !ok {
			nx = &Node{}
			n.ch[c] = nx
		}
		n = nx
	}
	n.term = append(n.term, idx)
}

// Build computes fail links and flattens output chains, breadth-first
// so every fail target is fully processed before its referrers.
func (t *Trie) Build() {
	t.root.fail = t.root
	queue := make([]*Node, 0, 8)
	for _, c := range t.root.ch {
		c.fail = t.root
		queue = append(queue, c)
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		u.out = append(append([]int{}, u.term...), u.fail.out...)
		for c, v := range u.ch {
			f := u.fail
			for f != t.root {
				if nx, ok := f.ch[c]; ok {
					v.fail = nx
					break
				}
				f = f.fail
			}
			if v.fail == nil {
				if nx, ok := t.root.ch[c]; ok && nx != v {
					v.fail = nx
				} else {
					v.fail = t.root
				}
			}
			queue = append(queue, v)
		}
	}
}

// Child returns the edge target for byte c.
func (n *Node) Child(c byte) (*Node, bool) { nx, ok := n.ch[c]; return nx, ok }

// Fail returns the failure-link target (root's fail is root).
func (n *Node) Fail() *Node { return n.fail }

// Output returns all pattern indices to report at this node:
// its own terminal patterns first, then those along the fail chain.
func (n *Node) Output() []int { return n.out }
