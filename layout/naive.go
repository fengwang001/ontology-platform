package layout

// naiveNode is an independently written box model that ignores dirty markers
// entirely: every operation validates identically and every commit performs a
// full bottom-up recomputation from the root. It serves as the oracle in
// differential tests: after any serialized sequence, the incremental Tree
// must expose exactly the same sizes as this model.
type naiveNode struct {
	id       int64
	width    Mode
	height   Mode
	pad      int
	isolated bool
	parent   *naiveNode
	children []*naiveNode
	size     Size
}

// NaiveModel is the oracle counterpart of Tree.
type NaiveModel struct {
	nodes map[int64]*naiveNode
	root  *naiveNode
}

// NewNaiveModel mirrors NewTree.
func NewNaiveModel(rootID int64) *NaiveModel {
	r := &naiveNode{id: rootID, width: Mode{Kind: ModeContent}, height: Mode{Kind: ModeContent}}
	m := &NaiveModel{nodes: map[int64]*naiveNode{rootID: r}, root: r}
	m.fullLayout()
	return m
}

func (m *NaiveModel) mustNode(id int64) (*naiveNode, error) {
	if n, ok := m.nodes[id]; ok {
		return n, nil
	}
	return nil, &LayoutError{Kind: KindNodeNotFound, Msg: "naive: node not found"}
}

func (m *NaiveModel) fullLayout() {
	var rec func(n *naiveNode) Size
	rec = func(n *naiveNode) Size {
		w, h := 2*n.pad, 2*n.pad
		for _, c := range n.children {
			cs := rec(c)
			w += cs.W
			h += cs.H
		}
		if n.width.Kind == ModeFixed {
			w = n.width.Value
		}
		if n.height.Kind == ModeFixed {
			h = n.height.Value
		}
		n.size = Size{W: w, H: h}
		return n.size
	}
	rec(m.root)
}

// Create mirrors Tree.Create (the node exists but is unmeasured until insert).
func (m *NaiveModel) Create(id int64, w, h Mode, pad int, isolated bool) error {
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
	if _, ok := m.nodes[id]; ok {
		return &LayoutError{Kind: KindConflict, Msg: "naive: node already exists"}
	}
	m.nodes[id] = &naiveNode{id: id, width: w, height: h, pad: pad, isolated: isolated}
	return nil
}

// SetWidth mirrors Tree.SetWidth and always full-relayouts.
func (m *NaiveModel) SetWidth(id int64, mode Mode) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if err := validateMode(mode); err != nil {
		return err
	}
	n, err := m.mustNode(id)
	if err != nil {
		return err
	}
	n.width = mode
	m.fullLayout()
	return nil
}

// SetHeight mirrors Tree.SetHeight.
func (m *NaiveModel) SetHeight(id int64, mode Mode) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if err := validateMode(mode); err != nil {
		return err
	}
	n, err := m.mustNode(id)
	if err != nil {
		return err
	}
	n.height = mode
	m.fullLayout()
	return nil
}

// SetPadding mirrors Tree.SetPadding.
func (m *NaiveModel) SetPadding(id int64, v int) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	if v < 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "negative padding"}
	}
	n, err := m.mustNode(id)
	if err != nil {
		return err
	}
	n.pad = v
	m.fullLayout()
	return nil
}

// SetIsolated mirrors Tree.SetIsolated.
func (m *NaiveModel) SetIsolated(id int64, isolated bool) error {
	if id <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	n, err := m.mustNode(id)
	if err != nil {
		return err
	}
	n.isolated = isolated
	m.fullLayout()
	return nil
}

func nIndexOf(s []*naiveNode, c *naiveNode) int {
	for i, x := range s {
		if x == c {
			return i
		}
	}
	return -1
}

func nIsDesc(a, n *naiveNode) bool {
	for p := n; p != nil; p = p.parent {
		if p == a {
			return true
		}
	}
	return false
}

// Insert mirrors Tree.Insert.
func (m *NaiveModel) Insert(parentID, childID int64, index int) error {
	if parentID <= 0 || childID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	p, err := m.mustNode(parentID)
	if err != nil {
		return err
	}
	c, err := m.mustNode(childID)
	if err != nil {
		return err
	}
	if c.parent != nil {
		return &LayoutError{Kind: KindConflict, Msg: "naive insert: child has parent"}
	}
	if c == m.root {
		return &LayoutError{Kind: KindConflict, Msg: "naive insert: root"}
	}
	if index < 0 || index > len(p.children) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "naive insert: bad index"}
	}
	if nIsDesc(c, p) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "naive insert: cycle"}
	}
	p.children = append(p.children, nil)
	copy(p.children[index+1:], p.children[index:])
	p.children[index] = c
	c.parent = p
	m.fullLayout()
	return nil
}

// Remove mirrors Tree.Remove (detached nodes keep existing model state).
func (m *NaiveModel) Remove(childID int64) error {
	if childID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	n, err := m.mustNode(childID)
	if err != nil {
		return err
	}
	if n == m.root {
		return &LayoutError{Kind: KindConflict, Msg: "naive remove: root"}
	}
	if n.parent == nil {
		return &LayoutError{Kind: KindConflict, Msg: "naive remove: detached"}
	}
	p := n.parent
	i := nIndexOf(p.children, n)
	p.children = append(p.children[:i], p.children[i+1:]...)
	n.parent = nil
	m.fullLayout()
	return nil
}

// Move mirrors Tree.Move.
func (m *NaiveModel) Move(childID, newParentID int64, index int) error {
	if childID <= 0 || newParentID <= 0 {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	n, err := m.mustNode(childID)
	if err != nil {
		return err
	}
	np, err := m.mustNode(newParentID)
	if err != nil {
		return err
	}
	if n == m.root {
		return &LayoutError{Kind: KindConflict, Msg: "naive move: root"}
	}
	if nIsDesc(n, np) {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "naive move: cycle"}
	}
	old := n.parent
	maxIndex := len(np.children)
	if old == np {
		maxIndex--
	}
	if index < 0 || index > maxIndex {
		return &LayoutError{Kind: KindInvalidArgument, Msg: "naive move: bad index"}
	}
	if old != nil {
		i := nIndexOf(old.children, n)
		old.children = append(old.children[:i], old.children[i+1:]...)
	}
	np.children = append(np.children, nil)
	copy(np.children[index+1:], np.children[index:])
	np.children[index] = n
	n.parent = np
	m.fullLayout()
	return nil
}

// Commit on the oracle is a no-op that simply returns nil (layout is always
// current). It exists so random sequences can call it on both models.
func (m *NaiveModel) Commit() error { return nil }

// SizeOf returns the full-recomputed size of node id.
func (m *NaiveModel) SizeOf(id int64) (Size, error) {
	if id <= 0 {
		return Size{}, &LayoutError{Kind: KindInvalidArgument, Msg: "invalid node id"}
	}
	n, err := m.mustNode(id)
	if err != nil {
		return Size{}, err
	}
	return n.size, nil
}
