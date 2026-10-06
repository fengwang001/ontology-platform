package mirror

// treapNode 是以块号为键、以确定性哈希为堆优先级的树堆节点。
// 优先级只取决于块号，因此同一组块号无论以何顺序插入，
// 最终结构完全一致，保证行为可精确复现。
type treapNode struct {
	key         int
	prio        uint64
	left, right *treapNode
}

// priorityOf 使用 splitmix64 由块号确定性地导出优先级。
func priorityOf(key int) uint64 {
	x := uint64(key) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// blockSet 是块号的有序集合，插入、删除、弹出最小元的开销
// 只随集合内元素数增长（期望 O(log d)），与卷的总块数无关。
// ops 非空时把节点访问次数计入该计数器，用于开销的外部验证。
type blockSet struct {
	root *treapNode
	size int
	ops  *uint64
}

func (s *blockSet) touch() {
	if s.ops != nil {
		*s.ops++
	}
}

func (s *blockSet) len() int { return s.size }

func (s *blockSet) contains(key int) bool {
	for n := s.root; n != nil; {
		s.touch()
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return true
		}
	}
	return false
}

func (s *blockSet) insert(key int) {
	if s.contains(key) {
		return
	}
	s.root = insertNode(s.root, &treapNode{key: key, prio: priorityOf(key)}, s)
	s.size++
}

func insertNode(n, x *treapNode, s *blockSet) *treapNode {
	if n == nil {
		return x
	}
	s.touch()
	if x.key < n.key {
		n.left = insertNode(n.left, x, s)
		if n.left.prio < n.prio {
			return rotateRight(n)
		}
		return n
	}
	n.right = insertNode(n.right, x, s)
	if n.right.prio < n.prio {
		return rotateLeft(n)
	}
	return n
}

func (s *blockSet) remove(key int) {
	var removed bool
	s.root, removed = removeNode(s.root, key, s)
	if removed {
		s.size--
	}
}

func removeNode(n *treapNode, key int, s *blockSet) (*treapNode, bool) {
	if n == nil {
		return nil, false
	}
	s.touch()
	switch {
	case key < n.key:
		var r bool
		n.left, r = removeNode(n.left, key, s)
		return n, r
	case key > n.key:
		var r bool
		n.right, r = removeNode(n.right, key, s)
		return n, r
	default:
		return mergeNodes(n.left, n.right, s), true
	}
}

func mergeNodes(a, b *treapNode, s *blockSet) *treapNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	s.touch()
	if a.prio < b.prio {
		a.right = mergeNodes(a.right, b, s)
		return a
	}
	b.left = mergeNodes(a, b.left, s)
	return b
}

// popMin 弹出集合中最小的块号，供重同步按块号升序推进。
func (s *blockSet) popMin() (int, bool) {
	if s.root == nil {
		return 0, false
	}
	n := s.root
	for n.left != nil {
		s.touch()
		n = n.left
	}
	key := n.key
	s.remove(key)
	return key, true
}

// ascending 以升序返回全部元素，仅用于快照与测试。
func (s *blockSet) ascending() []int {
	out := make([]int, 0, s.size)
	var walk func(n *treapNode)
	walk = func(n *treapNode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.key)
		walk(n.right)
	}
	walk(s.root)
	return out
}

func rotateRight(n *treapNode) *treapNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *treapNode) *treapNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}
