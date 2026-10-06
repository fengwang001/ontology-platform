package layout

// isBoundary reports whether n acts as a layout boundary. A node is a
// boundary iff it declares layout isolation and both dimensions are
// fixed-sized. The root is a boundary regardless of its declaration.
func isBoundary(n *node) bool {
	if n.parent == nil {
		return true
	}
	return n.isolated && n.width.Kind == ModeFixed && n.height.Kind == ModeFixed
}

// markSelf marks n self-dirty and propagates subtree-dirty to ancestors up to
// the nearest boundary. Cost is O(depth to nearest boundary), independent of
// total tree size: an ancestor that already carries subDirty either has a
// pending boundary above it or is itself the stopping boundary, so its own
// ancestor chain is already marked.
func (t *Tree) markSelf(n *node) {
	n.selfDirty = true
	if n == t.root {
		t.enqueueBoundary(t.root)
		return
	}
	for p := n.parent; p != nil; p = p.parent {
		if p.subDirty {
			return
		}
		p.subDirty = true
		if isBoundary(p) {
			t.enqueueBoundary(p)
			return
		}
	}
	// The parent chain ended above a detached subtree: nothing in the
	// committed tree is affected; the dirt stays inside the detached subtree.
}

// escapedDirty reports whether dirt inside n's subtree must propagate past n
// to its new ancestors after a move. selfDirty escapes; subDirty escapes
// unless n itself is a boundary (then the dirt is contained inside n).
func escapedDirty(n *node) bool {
	if n.selfDirty {
		return true
	}
	return n.subDirty && !isBoundary(n)
}

// markSubOnly marks the ancestor chain above n subtree-dirty without touching
// n itself. It is used after inserting a subtree that already carries dirt.
func (t *Tree) markSubOnly(start *node) {
	for p := start; p != nil; p = p.parent {
		if p.subDirty {
			return
		}
		p.subDirty = true
		if isBoundary(p) {
			t.enqueueBoundary(p)
			return
		}
	}
}

// enqueueBoundary records a non-root dirty boundary. The root is implicit and
// is never stored here, keeping commit work independent of clean tree size.
func (t *Tree) enqueueBoundary(b *node) {
	if b.parent != nil {
		t.bounds[b] = struct{}{}
	}
}

// clearDirty wipes all dirt after a commit and resets the boundary set.
func (t *Tree) clearDirty() {
	t.bounds = map[*node]struct{}{}
}
