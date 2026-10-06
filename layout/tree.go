package layout

import (
	"fmt"
	"strconv"
)

// SetWidth changes the width mode of node id. Equal values are a no-op.
func (t *Tree) SetWidth(id int64, m Mode) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if err := validateMode(m); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return err
	}
	if n.width == m {
		t.logDecision("setWidth", id, "unchanged: same value, no dirt")
		return nil
	}
	t.logInput("SetWidth", map[string]any{"id": id, "mode": modeDesc(m)})
	wasBoundary := isBoundary(n)
	n.width = m
	t.markSelf(n)
	t.afterBoundaryIdentityChange(n, wasBoundary)
	return nil
}

// SetHeight changes the height mode of node id. Equal values are a no-op.
func (t *Tree) SetHeight(id int64, m Mode) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if err := validateMode(m); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return err
	}
	if n.height == m {
		t.logDecision("setHeight", id, "unchanged: same value, no dirt")
		return nil
	}
	t.logInput("SetHeight", map[string]any{"id": id, "mode": modeDesc(m)})
	wasBoundary := isBoundary(n)
	n.height = m
	t.markSelf(n)
	t.afterBoundaryIdentityChange(n, wasBoundary)
	return nil
}

// SetPadding changes node id's uniform padding. Equal values are a no-op.
func (t *Tree) SetPadding(id int64, v int) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if v < 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "negative padding"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return err
	}
	if n.pad == v {
		t.logDecision("setPadding", id, "unchanged: same value, no dirt")
		return nil
	}
	t.logInput("SetPadding", map[string]any{"id": id, "padding": v})
	n.pad = v
	t.markSelf(n)
	return nil
}

// SetIsolated changes the layout-isolation declaration of node id.
func (t *Tree) SetIsolated(id int64, isolated bool) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(id)
	if err != nil {
		return err
	}
	if n.isolated == isolated {
		t.logDecision("setIsolated", id, "unchanged: same value, no dirt")
		return nil
	}
	t.logInput("SetIsolated", map[string]any{"id": id, "isolated": isolated})
	wasBoundary := isBoundary(n)
	n.isolated = isolated
	t.markSelf(n)
	t.afterBoundaryIdentityChange(n, wasBoundary)
	return nil
}

// afterBoundaryIdentityChange repairs propagation range after a change that
// may have flipped n's boundary identity.
//
// Loss: dirt that used to stop at n must continue upward. We do not
// distinguish stale marks from fresh ones, which can only over-propagate to
// an ancestor (harmless: the damage-walk stops early when sizes stop
// changing).
//
// Gain: marks already propagated to ancestors are intentionally left in
// place (invalidation never retracts), but n itself becomes a stopping
// boundary for any future propagation.
func (t *Tree) afterBoundaryIdentityChange(n *node, was bool) {
	now := isBoundary(n)
	if was == now {
		return
	}
	if was && !now {
		t.logDecision("boundaryLost", n.id, "subtree dirt re-propagates upward")
		t.markSubOnly(n.parent)
	} else {
		t.logDecision("boundaryGained", n.id, "ancestor marks retained; future dirt stops here")
	}
}

// Create adds a new unattached node. It starts self-dirty; nothing propagates
// until it is inserted.
func (t *Tree) Create(id int64, w, h Mode, pad int, isolated bool) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if err := validateMode(w); err != nil {
		return err
	}
	if err := validateMode(h); err != nil {
		return err
	}
	if pad < 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "negative padding"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.nodes[id]; ok {
		return &LayoutError{Kind: KindConflict, Msg: "node already exists: " + strconv.FormatInt(id, 10)}
	}
	n := &node{
		id:        id,
		width:     w,
		height:    h,
		pad:       pad,
		isolated:  isolated,
		selfDirty: true,
	}
	t.nodes[id] = n
	t.logInput("Create", map[string]any{"id": id, "width": modeDesc(w), "height": modeDesc(h), "pad": pad, "isolated": isolated})
	return nil
}

// Insert attaches child (an unattached node) to parent at index. An index
// equal to the current child count appends.
func (t *Tree) Insert(parentID, childID int64, index int) error {
	if parentID <= 0 || childID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := t.mustNode(parentID)
	if err != nil {
		return err
	}
	c, err := t.mustNode(childID)
	if err != nil {
		return err
	}
	if isDescendant(c, p) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "insert: node would become its own descendant"}
	}
	if c.parent != nil {
		return &LayoutError{Kind: KindConflict, Msg: "insert: child already has a parent"}
	}
	if c == t.root {
		return &LayoutError{Kind: KindConflict, Msg: "insert: root cannot be attached"}
	}
	if index < 0 || index > len(p.children) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "insert: index out of range"}
	}
	t.logInput("Insert", map[string]any{"parent": parentID, "child": childID, "index": index})
	p.children = insertChild(p.children, c, index)
	c.parent = p
	t.markSelf(p)
	if escapedDirty(c) {
		// Dirt carried by the moved-in subtree propagates from its new
		// location; c's own marks are preserved untouched.
		t.markSubOnly(p)
	}
	return nil
}

// Remove detaches childID from its parent. The detached subtree and all of
// its dirty marks are retained across removal.
func (t *Tree) Remove(childID int64) error {
	if childID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(childID)
	if err != nil {
		return err
	}
	if n == t.root {
		return &LayoutError{Kind: KindConflict, Msg: "remove: root cannot be removed"}
	}
	if n.parent == nil {
		return &LayoutError{Kind: KindConflict, Msg: "remove: node is not attached"}
	}
	t.logInput("Remove", map[string]any{"child": childID})
	p := n.parent
	idx := indexOf(p.children, n)
	p.children = append(p.children[:idx], p.children[idx+1:]...)
	n.parent = nil
	t.markSelf(p)
	return nil
}

// Move detaches childID and reattaches it under newParent at index. The moved
// subtree keeps its dirty marks; propagation is replayed at the new location
// whenever the subtree carries escaping dirt or crosses a boundary.
func (t *Tree) Move(childID, newParentID int64, index int) error {
	if childID <= 0 || newParentID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNode(childID)
	if err != nil {
		return err
	}
	np, err := t.mustNode(newParentID)
	if err != nil {
		return err
	}
	if n == t.root {
		return &LayoutError{Kind: KindConflict, Msg: "move: root cannot be moved"}
	}
	if isDescendant(n, np) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "move: node would become its own descendant"}
	}
	oldParent := n.parent
	maxIndex := len(np.children)
	if oldParent == np {
		maxIndex-- // one slot disappears while n is detached
	}
	if index < 0 || index > maxIndex {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "move: index out of range"}
	}
	t.logInput("Move", map[string]any{"child": childID, "newParent": newParentID, "index": index})
	if oldParent != nil {
		idx := indexOf(oldParent.children, n)
		oldParent.children = append(oldParent.children[:idx], oldParent.children[idx+1:]...)
		t.markSelf(oldParent)
	}
	np.children = insertChild(np.children, n, index)
	n.parent = np
	t.markSelf(np)
	if escapedDirty(n) {
		t.markSubOnly(np)
	}
	return nil
}

func insertChild(s []*node, c *node, i int) []*node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = c
	return s
}

func indexOf(s []*node, c *node) int {
	for i, x := range s {
		if x == c {
			return i
		}
	}
	return -1
}

func isDescendant(maybeAncestor, n *node) bool {
	for p := n; p != nil; p = p.parent {
		if p == maybeAncestor {
			return true
		}
	}
	return false
}

func modeDesc(m Mode) string {
	if m.Kind == ModeFixed {
		return fmt.Sprintf("fixed(%d)", m.Value)
	}
	return "content"
}
