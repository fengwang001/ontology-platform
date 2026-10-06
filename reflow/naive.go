package reflow

// naiveNode 是朴素模型的节点：只存当前属性与结构，不含任何脏标记，
// 提交时从根全量后序重算。与增量内核共享 Mode/Size 等值类型。
type naiveNode struct {
	id            NodeID
	parent        *naiveNode
	children      []*naiveNode
	isolated      bool
	width, height Mode
	padX, padY    int64
	w, h          int64
}

// Naive 是独立编写的全量重算朴素模型：不做脏标记、不做最小子树，
// 每次提交对所有主树节点及所有摘下子树各做一次完整后序重算。
type Naive struct {
	root  *naiveNode
	nodes map[NodeID]*naiveNode
	next  NodeID
}

// NewNaive 与 NewKernel 参数一致。
func NewNaive(width, height Mode, padX, padY int64) (*Naive, error) {
	if err := validateGeom("NewNaive", width, height, padX, padY); err != nil {
		return nil, err
	}
	m := &Naive{nodes: make(map[NodeID]*naiveNode)}
	m.root = &naiveNode{id: 0, width: width, height: height, padX: padX, padY: padY}
	m.nodes[0] = m.root
	m.fullCompute()
	return m, nil
}

func (m *Naive) Root() NodeID { return 0 }

func (m *Naive) NewNode(width, height Mode, padX, padY int64, isolated bool) (NodeID, error) {
	if err := validateGeom("NewNaive", width, height, padX, padY); err != nil {
		return 0, err
	}
	id := m.next + 1
	m.next = id
	n := &naiveNode{id: id, width: width, height: height, padX: padX, padY: padY, isolated: isolated}
	m.nodes[id] = n
	m.computeOne(n)
	return id, nil
}

func (m *Naive) lookup(id NodeID) (*naiveNode, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, kerr(KindNodeNotFound, "Naive", "节点 %d 不存在", id)
	}
	return n, nil
}

func (m *Naive) SetWidth(id NodeID, mode Mode) error {
	if err := validateGeom("Naive.SetWidth", mode, Auto(), 0, 0); err != nil {
		return err
	}
	n, err := m.lookup(id)
	if err != nil {
		return err
	}
	n.width = mode
	return nil
}

func (m *Naive) SetHeight(id NodeID, mode Mode) error {
	if err := validateGeom("Naive.SetHeight", Auto(), mode, 0, 0); err != nil {
		return err
	}
	n, err := m.lookup(id)
	if err != nil {
		return err
	}
	n.height = mode
	return nil
}

func (m *Naive) SetPadding(id NodeID, x, y int64) error {
	if x < 0 || y < 0 {
		return kerr(KindInvalidArg, "Naive.SetPadding", "负内边距 (%d,%d)", x, y)
	}
	n, err := m.lookup(id)
	if err != nil {
		return err
	}
	n.padX, n.padY = x, y
	return nil
}

func (m *Naive) SetIsolated(id NodeID, isolated bool) error {
	n, err := m.lookup(id)
	if err != nil {
		return err
	}
	n.isolated = isolated
	return nil
}

func (m *Naive) Insert(parentID, childID NodeID, index int) error {
	if index < 0 {
		return kerr(KindInvalidArg, "Naive.Insert", "负下标 %d", index)
	}
	p, err := m.lookup(parentID)
	if err != nil {
		return err
	}
	c, err := m.lookup(childID)
	if err != nil {
		return err
	}
	if index > len(p.children) {
		return kerr(KindInvalidArg, "Naive.Insert", "下标越界")
	}
	if c == p || naiveAncestor(c, p) {
		return kerr(KindInvalidArg, "Naive.Insert", "节点不能作为自身子孙")
	}
	if c.parent != nil {
		return kerr(KindConflict, "Naive.Insert", "节点已有父节点")
	}
	p.children = append(p.children, nil)
	copy(p.children[index+1:], p.children[index:])
	p.children[index] = c
	c.parent = p
	return nil
}

func (m *Naive) Remove(id NodeID) error {
	n, err := m.lookup(id)
	if err != nil {
		return err
	}
	if n == m.root {
		return kerr(KindConflict, "Naive.Remove", "不能移除根节点")
	}
	if n.parent == nil {
		return kerr(KindConflict, "Naive.Remove", "节点已摘除")
	}
	old := n.parent
	for i, c := range old.children {
		if c == n {
			old.children = append(old.children[:i], old.children[i+1:]...)
			break
		}
	}
	n.parent = nil
	return nil
}

func (m *Naive) Move(childID, newParentID NodeID, index int) error {
	if index < 0 {
		return kerr(KindInvalidArg, "Naive.Move", "负下标 %d", index)
	}
	c, err := m.lookup(childID)
	if err != nil {
		return err
	}
	p, err := m.lookup(newParentID)
	if err != nil {
		return err
	}
	if c == m.root {
		return kerr(KindConflict, "Naive.Move", "不能移动根节点")
	}
	if c == p || naiveAncestor(c, p) {
		return kerr(KindInvalidArg, "Naive.Move", "节点不能作为自身子孙")
	}
	if c.parent == nil {
		return kerr(KindConflict, "Naive.Move", "节点已摘除")
	}
	if c.parent == p {
		return kerr(KindConflict, "Naive.Move", "已在该父节点下")
	}
	if index > len(p.children) {
		return kerr(KindInvalidArg, "Naive.Move", "下标越界")
	}
	old := c.parent
	for i, x := range old.children {
		if x == c {
			old.children = append(old.children[:i], old.children[i+1:]...)
			break
		}
	}
	p.children = append(p.children, nil)
	copy(p.children[index+1:], p.children[index:])
	p.children[index] = c
	c.parent = p
	return nil
}

func naiveAncestor(a, n *naiveNode) bool {
	for p := n; p != nil; p = p.parent {
		if p == a {
			return true
		}
	}
	return false
}

// Commit 是朴素模型核心：不看任何标记，对主树做一次完整后序全量重算。
// 摘下的子树与增量内核一样冻结，直到重新插入后的下一次提交。
func (m *Naive) Commit() {
	m.fullCompute()
}

func (m *Naive) fullCompute() {
	var post func(n *naiveNode)
	post = func(n *naiveNode) {
		for _, c := range n.children {
			post(c)
		}
		m.computeOne(n)
	}
	post(m.root)
}

func (m *Naive) computeOne(n *naiveNode) {
	if n.width.Kind == ModeFixed {
		n.w = n.width.Value
	} else {
		var sum int64
		for _, c := range n.children {
			sum += c.w
		}
		n.w = 2*n.padX + sum
	}
	if n.height.Kind == ModeFixed {
		n.h = n.height.Value
	} else {
		var sum int64
		for _, c := range n.children {
			sum += c.h
		}
		n.h = 2*n.padY + sum
	}
}

func (m *Naive) Size(id NodeID) (Size, error) {
	n, err := m.lookup(id)
	if err != nil {
		return Size{}, err
	}
	return Size{W: n.w, H: n.h}, nil
}
