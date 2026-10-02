package acl

import "sync"

// node 是资源树节点。
type node struct {
	id        string
	parent    *node
	children  map[string]*node
	container bool
	depth     int
	aces      []ACE
	protected bool
}

// Store 维护资源树与各节点的显式条目，可并发使用。
type Store struct {
	mu       sync.Mutex
	maxDepth int
	maxACEs  int
	root     *node
	nodes    map[string]*node
	version  uint64

	lastEvalEntries int // 最近一次 Eval 处理的条目数
	lastEvalNodes   int // 最近一次 Eval 访问的节点数
}

// NewStore 构造求值器，D 为深度上限（1..64），K 为单节点显式条目数上限（1..64）。
func NewStore(D, K int) (*Store, error) {
	if D < 1 || D > maxDepthLim {
		return nil, newError(ErrInvalid, "depth limit %d out of range [1, %d]", D, maxDepthLim)
	}
	if K < 1 || K > maxACEsLim {
		return nil, newError(ErrInvalid, "ACE count limit %d out of range [1, %d]", K, maxACEsLim)
	}
	root := &node{id: "/", container: true, children: map[string]*node{}}
	return &Store{
		maxDepth: D,
		maxACEs:  K,
		root:     root,
		nodes:    map[string]*node{"/": root},
	}, nil
}

// Version 返回成功修改的累计次数。
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// AddNode 在已存在的容器 parent 下新增节点。
func (s *Store) AddNode(id, parent string, container bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return newError(ErrInvalid, "node id is empty")
	}
	p, ok := s.nodes[parent]
	if !ok {
		return newError(ErrNotFound, "parent node %q does not exist", parent)
	}
	if id == "/" {
		return newError(ErrConflict, "node id %q is reserved for the root", id)
	}
	if _, dup := s.nodes[id]; dup {
		return newError(ErrConflict, "node id %q already exists", id)
	}
	if !p.container {
		return newError(ErrConflict, "parent node %q is an object and cannot have children", parent)
	}
	if p.depth+1 > s.maxDepth {
		return newError(ErrLimit, "node %q would exceed depth limit %d", id, s.maxDepth)
	}
	n := &node{
		id:        id,
		parent:    p,
		children:  map[string]*node{},
		container: container,
		depth:     p.depth + 1,
	}
	p.children[id] = n
	s.nodes[id] = n
	s.version++
	return nil
}

// SetACL 原子地整体替换节点的显式条目列表与保护开关。
func (s *Store) SetACL(nodeID string, aces []ACE, protected bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range aces {
		if verr := validateACE(a); verr != nil {
			return newError(ErrInvalid, "ace %d on node %q: %s", i, nodeID, verr.Msg)
		}
	}
	n, ok := s.nodes[nodeID]
	if !ok {
		return newError(ErrNotFound, "node %q does not exist", nodeID)
	}
	if len(aces) > s.maxACEs {
		return newError(ErrLimit, "node %q has %d ACEs, limit is %d", nodeID, len(aces), s.maxACEs)
	}
	cp := make([]ACE, len(aces))
	copy(cp, aces)
	n.aces = cp
	n.protected = protected
	s.version++
	return nil
}

// Move 把非根节点连同子树改挂到另一容器之下。
func (s *Store) Move(nodeID, newParent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[nodeID]
	if !ok {
		return newError(ErrNotFound, "node %q does not exist", nodeID)
	}
	np, ok := s.nodes[newParent]
	if !ok {
		return newError(ErrNotFound, "new parent node %q does not exist", newParent)
	}
	if n == s.root {
		return newError(ErrConflict, "cannot move the root node")
	}
	if !np.container {
		return newError(ErrConflict, "new parent node %q is an object and cannot have children", newParent)
	}
	for cur := np; cur != nil; cur = cur.parent {
		if cur == n {
			return newError(ErrConflict, "new parent %q is the node itself or its descendant", newParent)
		}
	}
	if n.parent == np {
		return newError(ErrConflict, "new parent %q is already the current parent", newParent)
	}
	delta := np.depth + 1 - n.depth
	if subtreeMaxDepth(n)+delta > s.maxDepth {
		return newError(ErrLimit, "moving %q under %q would exceed depth limit %d", nodeID, newParent, s.maxDepth)
	}
	delete(n.parent.children, n.id)
	n.parent = np
	np.children[n.id] = n
	shiftDepth(n, delta)
	s.version++
	return nil
}

// validateACE 校验单条显式条目。
func validateACE(a ACE) *Error {
	if a.Principal == "" {
		return newError(ErrInvalid, "principal is empty")
	}
	if a.Mask < 1 || a.Mask > maxMask {
		return newError(ErrInvalid, "mask %d out of range [1, %d]", a.Mask, maxMask)
	}
	if a.Flags > FlagOI|FlagCI|FlagNP|FlagIO {
		return newError(ErrInvalid, "flags %d out of range [0, 15]", a.Flags)
	}
	if a.Flags&(FlagIO|FlagNP) != 0 && a.Flags&(FlagOI|FlagCI) == 0 {
		return newError(ErrInvalid, "flags %d: IO or NP requires at least one of OI or CI", a.Flags)
	}
	return nil
}

// subtreeMaxDepth 返回子树中的最大深度。
func subtreeMaxDepth(n *node) int {
	max := n.depth
	for _, c := range n.children {
		if d := subtreeMaxDepth(c); d > max {
			max = d
		}
	}
	return max
}

// shiftDepth 把子树中所有节点的深度平移 delta。
func shiftDepth(n *node, delta int) {
	n.depth += delta
	for _, c := range n.children {
		shiftDepth(c, delta)
	}
}
