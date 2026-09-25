package btree

import "ontology/internal/bnode"

// Snapshot returns copies of the root keys and every leaf's keys (left to
// right) under one read lock. It is an internal inspection aid for the demo;
// the public api exposes neither it nor the visited counter.
func (t *Tree) Snapshot() (rootKeys []int, leaves [][]int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.root == nil {
		return []int{}, nil
	}
	rootKeys = append([]int(nil), t.root.Keys...)
	var walk func(n *bnode.Node)
	walk = func(n *bnode.Node) {
		if n.IsLeaf() {
			leaves = append(leaves, append([]int(nil), n.Keys...))
			return
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	walk(t.root)
	return rootKeys, leaves
}

// insertRec descends to the owning leaf and unwinds splits upward. It returns
// (left, promoted, right, split, err). A duplicate is found before mutation,
// so the error path leaves the tree untouched.
func insertRec(n *bnode.Node, k int, vis, sp *int) (*bnode.Node, int, *bnode.Node, bool, error) {
	*vis++
	i := n.FindIndex(k)
	if i < len(n.Keys) && n.Keys[i] == k {
		return nil, 0, nil, false, ErrDuplicateKey
	}
	if n.IsLeaf() {
		n.Keys = insertAt(n.Keys, i, k)
	} else {
		l, m, r, split, err := insertRec(n.Kids[i], k, vis, sp)
		if err != nil {
			return nil, 0, nil, false, err
		}
		if split {
			n.Keys = insertAt(n.Keys, i, m)
			n.Kids[i] = l
			n.Kids = insertKidAt(n.Kids, i+1, r)
		}
	}
	if len(n.Keys) < 3 {
		return n, 0, nil, false, nil
	}
	*sp++ // 3 keys: index-1 key promotes; min/max go to left/right
	m := n.Keys[1]
	if n.IsLeaf() {
		return &bnode.Node{Keys: []int{n.Keys[0]}}, m, &bnode.Node{Keys: []int{n.Keys[2]}}, true, nil
	}
	l := &bnode.Node{Keys: []int{n.Keys[0]}, Kids: []*bnode.Node{n.Kids[0], n.Kids[1]}}
	r := &bnode.Node{Keys: []int{n.Keys[2]}, Kids: []*bnode.Node{n.Kids[2], n.Kids[3]}}
	return l, m, r, true, nil
}

// deleteRec removes k and reports (child underflowed to 0 keys, key found).
func deleteRec(n *bnode.Node, k int, vis, repairs *int) (bool, bool) {
	*vis++
	i := n.FindIndex(k)
	if n.IsLeaf() {
		if i == len(n.Keys) || n.Keys[i] != k {
			return false, false
		}
		n.Keys = append(n.Keys[:i], n.Keys[i+1:]...)
		return len(n.Keys) == 0, true
	}
	var under, found bool
	if i < len(n.Keys) && n.Keys[i] == k {
		p := maxKey(n.Kids[i])
		n.Keys[i] = p // predecessor replaces the deleted internal separator
		under, found = deleteRec(n.Kids[i], p, vis, repairs)
	} else {
		under, found = deleteRec(n.Kids[i], k, vis, repairs)
	}
	if found && under {
		repair(n, i, repairs)
	}
	return len(n.Keys) == 0, found
}

// repair fixes 0-key child p.Kids[idx]: borrow from a >=2-key sibling if one
// exists, else merge with a sibling (separator moves down); cost += 1.
func repair(p *bnode.Node, idx int, cost *int) {
	c := p.Kids[idx]
	switch {
	case idx > 0 && len(p.Kids[idx-1].Keys) > 1: // borrow from left sibling
		s := p.Kids[idx-1]
		up := s.Keys[len(s.Keys)-1]
		c.Keys = append([]int{p.Keys[idx-1]}, c.Keys...)
		s.Keys, p.Keys[idx-1] = s.Keys[:len(s.Keys)-1], up
		if !s.IsLeaf() {
			c.Kids = append([]*bnode.Node{s.Kids[len(s.Kids)-1]}, c.Kids...)
			s.Kids = s.Kids[:len(s.Kids)-1]
		}
	case idx+1 < len(p.Kids) && len(p.Kids[idx+1].Keys) > 1: // borrow from right
		s := p.Kids[idx+1]
		up := s.Keys[0]
		c.Keys = append(c.Keys, p.Keys[idx])
		s.Keys, p.Keys[idx] = s.Keys[1:], up
		if !s.IsLeaf() {
			c.Kids = append(c.Kids, s.Kids[0])
			s.Kids = s.Kids[1:]
		}
	default: // no lendable sibling: merge, preferring the left one
		if idx > 0 {
			s := p.Kids[idx-1]
			s.Keys = append(append(s.Keys, p.Keys[idx-1]), c.Keys...)
			s.Kids = append(s.Kids, c.Kids...)
			p.Keys = append(p.Keys[:idx-1], p.Keys[idx:]...)
			p.Kids = append(p.Kids[:idx], p.Kids[idx+1:]...)
		} else {
			s := p.Kids[1]
			c.Keys = append(append(c.Keys, p.Keys[0]), s.Keys...)
			c.Kids = append(c.Kids, s.Kids...)
			p.Keys = p.Keys[1:]
			p.Kids = append(p.Kids[:1], p.Kids[2:]...)
		}
	}
	*cost++
}

func maxKey(n *bnode.Node) int {
	for !n.IsLeaf() {
		n = n.Kids[len(n.Kids)-1]
	}
	return n.Keys[len(n.Keys)-1]
}

func insertAt(s []int, i, k int) []int {
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = k
	return s
}

func insertKidAt(s []*bnode.Node, i int, k *bnode.Node) []*bnode.Node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = k
	return s
}
