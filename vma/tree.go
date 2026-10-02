package vma

// node is one immutable AVL node carrying a VMA and a subtree gap summary.
//
// Blocks in start order are: ordinary VMA -> [start,end); grows-down VMA ->
// [max(low,start-G), end). A block's guard part may overlap earlier blocks, so
// scanning left to right the current coverage position is cov = max over every
// block seen of (lo + positive extent): effectively the furthest covered page
// boundary encountered. The summary stores:
//
//	lastEnd  = furthest covered boundary after scanning the subtree
//	innerMax = largest gap ending at a block start strictly after the first
//	           block of the subtree (cov-independent)
type node struct {
	v           VMA
	left, right *node
	h           int
	firstLo     int64
	lastEnd     int64
	innerMax    int64
}

type tree struct {
	low   int64
	guard int64
}

func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.h
}

func (t *tree) blockLoOf(v VMA) int64 {
	if v.GrowsDown {
		lo := v.Start - t.guard
		if lo < t.low {
			lo = t.low
		}
		return lo
	}
	return v.Start
}

// make builds an immutable node and recomputes its summary.
func (t *tree) make(v VMA, l, r *node) *node {
	n := &node{v: v, left: l, right: r}
	n.h = 1 + height(l)
	if height(r) > n.h-1 {
		n.h = 1 + height(r)
	}

	lo := t.blockLoOf(v)
	end := v.End
	firstLo := lo
	innerMax := int64(0)

	// Left subtree first.
	if l != nil {
		firstLo = l.firstLo
		innerMax = l.innerMax
		lo = max64(lo, l.lastEnd)
		if l.lastEnd > end {
			end = l.lastEnd
		}
	}
	// Node block: incoming coverage position is lo (furthest end from left).
	nodeLo := t.blockLoOf(v)
	// The gap ending at the node block counts as inner unless the node is the
	// first block of this whole subtree (i.e. empty left subtree).
	if l != nil {
		pre := nodeLo - lo
		if pre < 0 {
			pre = 0
		}
		if pre > innerMax {
			innerMax = pre
		}
	}
	end = max64(v.End, lo)

	// Right subtree enters with coverage end.
	if r != nil {
		if r.firstLo < firstLo {
			firstLo = r.firstLo
		}
		innerMax = max64(innerMax, r.innerMax)
		firstRightGap := r.firstLo - end
		if firstRightGap < 0 {
			firstRightGap = 0
		}
		if firstRightGap > innerMax {
			innerMax = firstRightGap
		}
		if r.lastEnd > end {
			end = r.lastEnd
		}
	}

	n.firstLo = firstLo
	n.lastEnd = end
	n.innerMax = innerMax
	return n
}

func bf(n *node) int { return height(n.left) - height(n.right) }

func (t *tree) balance(v VMA, l, r *node) *node {
	n := t.make(v, l, r)
	switch {
	case bf(n) > 1:
		if bf(l) > 0 {
			return t.make(l.v, l.left, t.make(v, l.right, r))
		}
		lr := l.right
		return t.make(lr.v, t.make(l.v, l.left, lr.left), t.make(v, lr.right, r))
	case bf(n) < -1:
		if bf(r) < 0 {
			return t.make(r.v, t.make(v, l, r.left), r.right)
		}
		rl := r.left
		return t.make(rl.v, t.make(v, l, rl.left), t.make(r.v, rl.right, r.right))
	}
	return n
}

func (t *tree) insertAt(n *node, v VMA) *node {
	if n == nil {
		return t.make(v, nil, nil)
	}
	if v.Start < n.v.Start {
		return t.balance(n.v, t.insertAt(n.left, v), n.right)
	}
	return t.balance(n.v, n.left, t.insertAt(n.right, v))
}

func (t *tree) insert(root *node, v VMA, vis *int) *node {
	*vis++
	return t.insertAt(root, v)
}

func (t *tree) merge(l, r *node) *node {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.h > r.h {
		return t.make(l.v, l.left, t.merge(l.right, r))
	}
	if r.h > l.h {
		return t.make(r.v, t.merge(l, r.left), r.right)
	}
	m := r
	for m.left != nil {
		m = m.left
	}
	return t.make(m.v, l, t.eraseAt(r, m.v.Start))
}

func (t *tree) eraseAt(n *node, start int64) *node {
	if n == nil {
		return nil
	}
	if start < n.v.Start {
		return t.balance(n.v, t.eraseAt(n.left, start), n.right)
	}
	if start > n.v.Start {
		return t.balance(n.v, n.left, t.eraseAt(n.right, start))
	}
	return t.merge(n.left, n.right)
}

func (t *tree) erase(root *node, start int64, vis *int) *node {
	*vis++
	return t.eraseAt(root, start)
}

func (t *tree) find(root *node, addr int64, vis *int) *node {
	n := root
	for n != nil {
		*vis++
		switch {
		case addr < n.v.Start:
			n = n.left
		case addr >= n.v.End:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

func (t *tree) successor(root *node, addr int64, vis *int) *node {
	var res *node
	n := root
	for n != nil {
		*vis++
		if n.v.Start > addr {
			res = n
			n = n.left
		} else {
			n = n.right
		}
	}
	return res
}

func (t *tree) predecessor(root *node, addr int64, vis *int) *node {
	var res *node
	n := root
	for n != nil {
		*vis++
		if n.v.End <= addr {
			res = n
			n = n.right
		} else {
			n = n.left
		}
	}
	return res
}

func (t *tree) overlapper(root *node, start, end int64, vis *int) *node {
	n := root
	for n != nil {
		*vis++
		switch {
		case end <= n.v.Start:
			n = n.left
		case start >= n.v.End:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

func (tr *tree) inorder(n *node, out *[]VMA, vis *int) {
	if n == nil {
		return
	}
	*vis++
	tr.inorder(n.left, out, vis)
	*out = append(*out, n.v)
	tr.inorder(n.right, out, vis)
}

func (tr *tree) all(root *node, vis *int) []VMA {
	out := make([]VMA, 0)
	tr.inorder(root, &out, vis)
	return out
}

func treeCount(n *node) int {
	if n == nil {
		return 0
	}
	return 1 + treeCount(n.left) + treeCount(n.right)
}

// scanCovAfter returns the coverage boundary after scanning subtree n given
// incoming coverage position cov.
func (t *tree) scanCovAfter(n *node, cov int64) int64 {
	// lastEnd already captures the max block end; incoming cov only matters
	// when it exceeds everything.
	return max64(cov, n.lastEnd)
}

// gapSearch finds the rightmost free gap of at least length pages within
// [t.low, high) after all blocks are removed. Returns the gap [lo,hi).
// Traversal visits O(log n) nodes by pruning subtrees whose every gap is
// shorter than length.
func (t *tree) gapSearch(root *node, length, high int64, vis *int) (lo, hi int64, ok bool) {
	return t.gapSearchIn(root, t.low, high, length, vis)
}

// maxGapIn computes the largest gap present in subtree n when coverage
// position cov enters it and the hard top is top.
func (t *tree) maxGapIn(n *node, cov, top, length int64) bool {
	if n == nil {
		return top-cov >= length
	}
	// Tail gap.
	if top-n.lastEnd >= length {
		return true
	}
	// Inner pre-block gaps. maxPre is cov-independent except the first gap;
	// the first gap is firstLo - covIn where covIn >= cov, so checking
	// firstLo-cov is a necessary condition, and maxPre covers the rest.
	if n.innerMax >= length {
		return true
	}
	if n.firstLo-cov >= length {
		return true
	}
	return false
}

func (t *tree) gapSearchIn(n *node, cov, top, length int64, vis *int) (int64, int64, bool) {
	for n != nil {
		*vis++
		nodeLoRaw := t.blockLoOf(n.v)

		// 1) right subtree / its tail gap (rightmost).
		covInRight := max64(cov, n.v.End)
		if n.right != nil {
			if t.maxGapIn(n.right, covInRight, top, length) {
				if rlo, rhi, rok := t.gapSearchIn(n.right, covInRight, top, length, vis); rok {
					return rlo, rhi, true
				}
			}
		} else if top-n.v.End >= length {
			return n.v.End, top, true
		}

		// 2) gap ending at this node's block start (between left coverage and
		// the block). The gap is [covAfterLeft, nodeLoRaw).
		covAfterLeft := cov
		if n.left != nil {
			covAfterLeft = max64(covAfterLeft, n.left.lastEnd)
		}
		if nodeLoRaw-covAfterLeft >= length {
			return covAfterLeft, nodeLoRaw, true
		}

		// 3) left subtree; its tail ends at nodeLoRaw (block start), and its
		// incoming cov is unchanged.
		top = nodeLoRaw
		n = n.left
	}
	return cov, top, top-cov >= length
}
