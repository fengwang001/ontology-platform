package layout

import "sort"

// Change records one recomputed node with its old and new sizes.
type Change struct {
	NodeID int64
	Old    Size
	New    Size
}

// commit runs one explicit or implicit reflow. It recomputes only the
// minimal subtrees below dirty boundaries: recursion enters exclusively
// self/sub-dirty nodes, so its cost is independent of the number of clean
// nodes. Returns KindReentrantCommit when commit is already running.
func (t *Tree) commit() ([]Change, error) {
	if t.committing {
		err := &LayoutError{Kind: KindReentrantCommit, Msg: "commit reentered during commit"}
		t.logOutput("Commit", map[string]any{"rejected": err.Error()})
		return nil, err
	}
	t.committing = true
	defer func() { t.committing = false }()
	if t.onCommit != nil {
		t.onCommit()
	}

	if !t.hasDirt() {
		t.reflowN = 0
		t.logOutput("Commit", map[string]any{"recomputed": 0, "reason": "no dirty markers"})
		return nil, nil
	}

	changes := make([]Change, 0)
	visited := map[*node]bool{}
	for _, b := range t.dirtyBoundaryOrder() {
		if visited[b] || !(b.selfDirty || b.subDirty) {
			continue
		}
		t.logDecision("reflowBoundary", b.id, "minimal subtree recomputation starts")
		t.reflowNode(b, b, &changes, visited)
	}

	t.clearDirty()
	t.reflowN = len(changes)
	t.logOutput("Commit", map[string]any{"recomputed": len(changes), "changes": changes})
	return changes, nil
}

func (t *Tree) hasDirt() bool {
	return t.root.selfDirty || t.root.subDirty || len(t.bounds) > 0
}

// reflowNode recomputes n bottom-up and reports whether its size changed
// relative to the enclosing boundary b. Children precede parents; siblings
// follow tree order. A parent is recomputed when it is self-dirty or a child
// changed, and damage stops at the first parent whose resulting size is
// unchanged. Boundaries always report "unchanged upward" because both
// dimensions are fixed, containing damage within themselves.
func (t *Tree) reflowNode(b, n *node, changes *[]Change, visited map[*node]bool) bool {
	visited[n] = true
	old := n.size
	selfDirty := n.selfDirty
	n.selfDirty = false
	n.subDirty = false

	childChanged := false
	for _, c := range n.children {
		if !(c.selfDirty || c.subDirty) {
			continue
		}
		// A nested boundary is still recomputed here; it simply reports no
		// change upward because its dimensions are fixed. The visited set
		// prevents its independently scheduled top-level run from duplicating.
		if t.reflowNode(b, c, changes, visited) {
			childChanged = true
		}
	}

	if !selfDirty && !childChanged {
		return false
	}
	nw := computeSize(n)
	n.size = nw
	*changes = append(*changes, Change{NodeID: n.id, Old: old, New: nw})
	t.logDecision("recompute", n.id,
		"selfDirty="+boolStr(selfDirty)+" childChanged="+boolStr(childChanged)+
			" old="+sizeStr(old)+" new="+sizeStr(nw))

	if isBoundary(n) {
		return false // fixed dimensions: damage is contained here
	}
	return nw != old
}

// dirtyBoundaryOrder returns dirty boundaries in tree preorder. The root is
// first when dirty; non-root boundaries sort by sibling-index path. Key
// construction cost is O(depth) per boundary, independent of clean nodes.
func (t *Tree) dirtyBoundaryOrder() []*node {
	type keyed struct {
		n   *node
		key []int
	}
	var ks []keyed
	add := func(b *node) {
		if !(b.selfDirty || b.subDirty) {
			return
		}
		var key []int
		for x := b; x.parent != nil; x = x.parent {
			key = append(key, indexOf(x.parent.children, x))
		}
		ks = append(ks, keyed{b, key})
	}
	if t.root.selfDirty || t.root.subDirty {
		ks = append(ks, keyed{t.root, nil})
	}
	for b := range t.bounds {
		add(b)
	}
	sort.SliceStable(ks, func(i, j int) bool {
		a, b := ks[i].key, ks[j].key
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	out := make([]*node, len(ks))
	for i, k := range ks {
		out[i] = k.n
	}
	return out
}

// LastReflowCount reports how many nodes the most recent commit recomputed.
func (t *Tree) LastReflowCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reflowN
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func sizeStr(s Size) string {
	return itoa(s.W) + "x" + itoa(s.H)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
