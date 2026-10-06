package mesh

import "sort"

// candidate references one matcher of one rule inside the fast index.
type candidate struct {
	ruleIdx  int
	matchIdx int
}

// trieNode is one segment-boundary node. exact holds matchers requiring an
// exact path equal to this node's path; prefix holds segment-boundary
// prefix matchers rooted at this node.
type trieNode struct {
	children map[string]*trieNode
	exact    []candidate
	prefix   []candidate
}

func newTrieNode() *trieNode {
	return &trieNode{children: map[string]*trieNode{}}
}

// pathIndex is built once per publication. Lookup touches only nodes along
// the request's segment chain, so matchers rooted at unrelated paths are
// never examined.
type pathIndex struct {
	root     *trieNode
	matchAll []candidate // matchers without a path condition
}

func buildPathIndex(cfg *Config) *pathIndex {
	idx := &pathIndex{root: newTrieNode()}
	for ri := range cfg.Rules {
		for mi := range cfg.Rules[ri].Matchers {
			m := &cfg.Rules[ri].Matchers[mi]
			c := candidate{ruleIdx: ri, matchIdx: mi}
			if m.Path == nil {
				idx.matchAll = append(idx.matchAll, c)
				continue
			}
			node := idx.root
			for _, seg := range segments(m.Path.Value) {
				next := node.children[seg]
				if next == nil {
					next = newTrieNode()
					node.children[seg] = next
				}
				node = next
			}
			if m.Path.Kind == PathExact {
				node.exact = append(node.exact, c)
			} else {
				node.prefix = append(node.prefix, c)
			}
		}
	}
	sortCandidates := func(cs []candidate) {
		sort.Slice(cs, func(a, b int) bool {
			if cs[a].ruleIdx != cs[b].ruleIdx {
				return cs[a].ruleIdx < cs[b].ruleIdx
			}
			return cs[a].matchIdx < cs[b].matchIdx
		})
	}
	sortCandidates(idx.matchAll)
	var walk func(n *trieNode)
	walk = func(n *trieNode) {
		sortCandidates(n.exact)
		sortCandidates(n.prefix)
		for _, ch := range n.children {
			walk(ch)
		}
	}
	walk(idx.root)
	return idx
}

// candidatesFor returns candidate matchers whose path condition CAN match
// the given stripped path, ordered by rule index. Matchers whose path
// cannot match are never enumerated.
func (idx *pathIndex) candidatesFor(strippedPath string) []candidate {
	segs := segments(strippedPath)
	out := make([]candidate, 0, len(idx.matchAll))
	out = append(out, idx.matchAll...)
	node := idx.root
	for i, seg := range segs {
		next := node.children[seg]
		if next == nil {
			break
		}
		node = next
		// Prefix matchers bound at every node on the chain match.
		out = append(out, node.prefix...)
		if i == len(segs)-1 {
			out = append(out, node.exact...)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].ruleIdx != out[b].ruleIdx {
			return out[a].ruleIdx < out[b].ruleIdx
		}
		return out[a].matchIdx < out[b].matchIdx
	})
	return out
}
