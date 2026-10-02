package ontology

type vmaNode struct {
	vma    VMA
	left   *vmaNode
	right  *vmaNode
	height int
}

type vmaTree struct {
	root    *vmaNode
	visited uint64
}

func (t *vmaTree) count() int {
	var walk func(*vmaNode) int
	walk = func(n *vmaNode) int {
		if n == nil {
			return 0
		}
		return 1 + walk(n.left) + walk(n.right)
	}
	return walk(t.root)
}

func (t *vmaTree) insert(v VMA) { t.root = insertVMA(t.root, v) }

func insertVMA(n *vmaNode, v VMA) *vmaNode {
	if n == nil {
		return &vmaNode{vma: v, height: 1}
	}
	if v.Start < n.vma.Start {
		n.left = insertVMA(n.left, v)
	} else {
		n.right = insertVMA(n.right, v)
	}
	updateVMA(n)
	return rebalanceVMA(n)
}

func (t *vmaTree) delete(start int64) { t.root = deleteVMA(t.root, start) }

func deleteVMA(n *vmaNode, start int64) *vmaNode {
	if n == nil {
		return nil
	}
	if start < n.vma.Start {
		n.left = deleteVMA(n.left, start)
	} else if start > n.vma.Start {
		n.right = deleteVMA(n.right, start)
	} else if n.left == nil {
		return n.right
	} else if n.right == nil {
		return n.left
	} else {
		next := n.right
		for next.left != nil {
			next = next.left
		}
		n.vma = next.vma
		n.right = deleteVMA(n.right, next.vma.Start)
	}
	updateVMA(n)
	return rebalanceVMA(n)
}

func (t *vmaTree) find(addr int64) (VMA, bool) {
	n := t.root
	for n != nil {
		t.visited++
		if addr < n.vma.Start {
			n = n.left
		} else if addr >= n.vma.End {
			n = n.right
		} else {
			return n.vma, true
		}
	}
	return VMA{}, false
}

func (t *vmaTree) nodeAt(start int64) *vmaNode {
	n := t.root
	for n != nil {
		t.visited++
		if start < n.vma.Start {
			n = n.left
		} else if start > n.vma.Start {
			n = n.right
		} else {
			return n
		}
	}
	return nil
}

func (t *vmaTree) floor(addr int64) (VMA, bool) {
	n := t.root
	var result *vmaNode
	for n != nil {
		t.visited++
		if addr < n.vma.Start {
			n = n.left
		} else {
			result = n
			n = n.right
		}
	}
	if result == nil {
		return VMA{}, false
	}
	return result.vma, true
}

func (t *vmaTree) lowerBound(addr int64) (VMA, bool) {
	n := t.root
	var result *vmaNode
	for n != nil {
		t.visited++
		if addr <= n.vma.Start {
			result = n
			n = n.left
		} else {
			n = n.right
		}
	}
	if result == nil {
		return VMA{}, false
	}
	return result.vma, true
}

func (t *vmaTree) list() []VMA {
	out := make([]VMA, 0)
	var walk func(*vmaNode)
	walk = func(n *vmaNode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.vma)
		walk(n.right)
	}
	walk(t.root)
	return out
}

func updateVMA(n *vmaNode) { n.height = 1 + maxInt(vmaHeight(n.left), vmaHeight(n.right)) }

func rebalanceVMA(n *vmaNode) *vmaNode {
	balance := vmaHeight(n.left) - vmaHeight(n.right)
	if balance > 1 {
		if vmaHeight(n.left.left) < vmaHeight(n.left.right) {
			n.left = rotateVMALeft(n.left)
		}
		return rotateVMARight(n)
	}
	if balance < -1 {
		if vmaHeight(n.right.right) < vmaHeight(n.right.left) {
			n.right = rotateVMARight(n.right)
		}
		return rotateVMALeft(n)
	}
	return n
}

func rotateVMARight(n *vmaNode) *vmaNode {
	x := n.left
	n.left = x.right
	x.right = n
	updateVMA(n)
	updateVMA(x)
	return x
}

func rotateVMALeft(n *vmaNode) *vmaNode {
	x := n.right
	n.right = x.left
	x.left = n
	updateVMA(n)
	updateVMA(x)
	return x
}

func vmaHeight(n *vmaNode) int {
	if n == nil {
		return 0
	}
	return n.height
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
