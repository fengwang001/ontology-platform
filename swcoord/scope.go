package swcoord

import "strings"

// splitPath turns "/a/b/" or "/a/b/page" into ["a", "b", ...],
// dropping empty segments. Scope "/x/" matches document URL "/x/y"
// iff the scope's segments are a prefix of the URL's segments, which
// is exactly string-prefix matching for slash-terminated scopes.
func splitPath(p string) []string {
	raw := strings.Split(p, "/")
	segs := raw[:0]
	for _, s := range raw {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

// scopeNode is one path segment in the scope trie. Lookups walk one
// node per URL segment, so match cost depends on URL depth only and
// never on the number of registered scopes.
type scopeNode struct {
	children map[string]*scopeNode
	reg      *Registration
}

type scopeTrie struct {
	root *scopeNode
}

func newScopeTrie() *scopeTrie {
	return &scopeTrie{root: &scopeNode{}}
}

func (t *scopeTrie) insert(scope string, reg *Registration) {
	node := t.root
	for _, seg := range splitPath(scope) {
		if node.children == nil {
			node.children = make(map[string]*scopeNode)
		}
		child, ok := node.children[seg]
		if !ok {
			child = &scopeNode{}
			node.children[seg] = child
		}
		node = child
	}
	node.reg = reg
}

func (t *scopeTrie) remove(scope string) {
	segs := splitPath(scope)
	var prune func(node *scopeNode, depth int) bool
	prune = func(node *scopeNode, depth int) bool {
		if depth == len(segs) {
			node.reg = nil
		} else {
			child, ok := node.children[segs[depth]]
			if !ok {
				return false
			}
			if prune(child, depth+1) {
				delete(node.children, segs[depth])
			}
		}
		return node.reg == nil && len(node.children) == 0
	}
	prune(t.root, 0)
}

// match returns the registration whose scope is the longest prefix of
// url, and the number of trie nodes visited (proof that lookup cost is
// bounded by URL depth, not registration count).
// Segment walking narrows candidates; the HasPrefix check enforces
// exact string-prefix semantics (scope "/a/" must not own URL "/a").
func (t *scopeTrie) match(url string) (*Registration, int) {
	node := t.root
	visits := 1
	var best *Registration
	if node.reg != nil && strings.HasPrefix(url, node.reg.scope) {
		best = node.reg
	}
	for _, seg := range splitPath(url) {
		child, ok := node.children[seg]
		if !ok {
			break
		}
		node = child
		visits++
		if node.reg != nil && strings.HasPrefix(url, node.reg.scope) {
			best = node.reg
		}
	}
	return best, visits
}
