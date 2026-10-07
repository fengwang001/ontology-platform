package sw

import "strings"

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

func splitPath(p string) []string {
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (t *scopeTrie) insert(scope string, r *Registration) {
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
	node.reg = r
}

func (t *scopeTrie) remove(scope string) {
	segs := splitPath(scope)
	var prune func(n *scopeNode, depth int) bool
	prune = func(n *scopeNode, depth int) bool {
		if depth == len(segs) {
			n.reg = nil
		} else {
			child := n.children[segs[depth]]
			if child == nil {
				return false
			}
			if prune(child, depth+1) {
				delete(n.children, segs[depth])
			}
		}
		return n.reg == nil && len(n.children) == 0
	}
	prune(t.root, 0)
}

func (t *scopeTrie) match(path string) (*Registration, int) {
	node := t.root
	steps := 1
	var best *Registration
	if node.reg != nil && !node.reg.pendingRemoval && strings.HasPrefix(path, node.reg.scope) {
		best = node.reg
	}
	for _, seg := range splitPath(path) {
		child, ok := node.children[seg]
		if !ok {
			break
		}
		node = child
		steps++
		if node.reg != nil && !node.reg.pendingRemoval && strings.HasPrefix(path, node.reg.scope) {
			best = node.reg
		}
	}
	return best, steps
}
