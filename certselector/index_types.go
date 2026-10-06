package certselector

import "sync"

// index is the concurrency-safe in-memory state. All mutations hold mu
// exclusively; a Select takes one read lock for its whole lookup, so it sees a
// consistent instant and can never observe a half-applied update. The
// exact-name hash map and the reversed-label wildcard trie bound selection
// cost to the matched name's labels and the matched candidate certificates.
type index struct {
	mu       sync.RWMutex
	certs    map[string]Certificate
	exact    map[string][]string // normalized exact name -> IDs
	wild     map[string][]string // wildcard base -> IDs
	wildRoot *trie               // reversed-label trie of wildcard bases
	defaults string
}

func newIndex() *index {
	return &index{
		certs:    map[string]Certificate{},
		exact:    map[string][]string{},
		wild:     map[string][]string{},
		wildRoot: newTrie(),
	}
}

// trie indexes wildcard bases by reversed labels. To find whether a name like
// "a.b.example.com" matches a wildcard "*.b.example.com", Select walks the
// name from the right ("com","example","b"); a terminal node reached after
// exactly len(labels)-1 steps identifies the matching base. Only the name's
// own labels are traversed, so the cost is independent of the number of
// wildcard certificates.
type trie struct {
	children map[string]*trie
	terminal bool
}

func newTrie() *trie { return &trie{children: map[string]*trie{}} }

func (t *trie) insert(labels []string) {
	node := t
	for i := len(labels) - 1; i >= 0; i-- {
		child := node.children[labels[i]]
		if child == nil {
			child = newTrie()
			node.children[labels[i]] = child
		}
		node = child
	}
	node.terminal = true
}

func (t *trie) remove(labels []string) {
	// Remove only the terminal marker; empty branches may remain, but their
	// terminal flags are false so they never produce false positives. This
	// keeps mutation O(labels) while wildcard lookup stays label-bounded.
	node := t
	for i := len(labels) - 1; i >= 0; i-- {
		child := node.children[labels[i]]
		if child == nil {
			return
		}
		node = child
	}
	node.terminal = false
}

// wildcardBase reports the wildcard base matched by labels when a terminal
// node is found after exactly len(labels)-1 right-to-left steps; ok is false
// otherwise (zero extra labels or two or more extra labels).
func (t *trie) wildcardBase(labels []string) (base string, ok bool) {
	node := t
	for i := len(labels) - 1; i >= 1; i-- {
		child := node.children[labels[i]]
		if child == nil {
			return "", false
		}
		node = child
	}
	if !node.terminal {
		return "", false
	}
	return joinLabels(labels[1:]), true
}
