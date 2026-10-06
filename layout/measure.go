package layout

// Commit triggers one explicit reflow and returns the ordered list of
// recomputed nodes with old/new sizes. Ordering: children before parents,
// siblings in tree order, independent dirty boundary subtrees in tree
// preorder of their boundaries.
func (t *Tree) Commit() ([]Change, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commit()
}

// SizeOf returns the committed size of node id. Pending dirty markers force
// an implicit commit first; the implicit commit's recomputation set and
// ordering are identical to an explicit Commit, and a clean query performs no
// recomputation.
func (t *Tree) SizeOf(id int64) (Size, error) {
	if id <= 0 {
		return Size{}, &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return Size{}, err
	}
	if t.hasDirt() {
		if _, err := t.commit(); err != nil {
			return Size{}, err
		}
	} else {
		t.reflowN = 0
		t.logDecision("querySkip", id, "no dirty markers: no recomputation")
	}
	return n.size, nil
}

// IsDirty reports whether node id currently carries self or subtree dirt.
func (t *Tree) IsDirty(id int64) (bool, error) {
	if id <= 0 {
		return false, &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return false, err
	}
	return n.selfDirty || n.subDirty, nil
}

// IsBoundary reports whether node id currently acts as a layout boundary.
func (t *Tree) IsBoundary(id int64) (bool, error) {
	if id <= 0 {
		return false, &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return false, err
	}
	return isBoundary(n), nil
}
