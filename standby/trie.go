package standby

// orderedSet 是按 128 位定长键排序的集合。
// 深度固定为 128，故插入/删除/取下一个键的耗时对集合规模为常数
// （与集合规模无关，仅取决于固定的机器字宽）。仅在 System 全局互斥内使用。
type orderedSet struct {
	root *trieNode
}

type trieNode struct {
	children [2]*trieNode
	leaf     *trieLeaf
	parent   *trieNode
	bit      byte // 本节点相对父节点走的分支
}

type trieLeaf struct {
	keyHi  uint64
	keyLo  uint64
	entry  *Entry
	parent *trieNode // 叶子所在的第 128 层内部节点，用于自底向上收缩
}

func newOrderedSet() *orderedSet {
	return &orderedSet{root: &trieNode{}}
}

// trieDepth 是键的二进制位数：(registered int64, id uint64) 拼成 128 位。
// 该深度对任何输入规模都是常数。
const trieDepth = 128

// bitAt 取 128 位键自高位起的第 d 位（d 从 0 开始）。
func bitAt(hi, lo uint64, d int) byte {
	if d < 64 {
		return byte((hi >> (63 - d)) & 1)
	}
	return byte((lo >> (127 - d)) & 1)
}

func (s *orderedSet) insert(leaf *trieLeaf) {
	node := s.root
	for d := 0; d < trieDepth; d++ {
		b := bitAt(leaf.keyHi, leaf.keyLo, d)
		next := node.children[b]
		if next == nil {
			next = &trieNode{parent: node, bit: b}
			node.children[b] = next
		}
		node = next
	}
	node.leaf = leaf
	leaf.parent = node
}

func (s *orderedSet) delete(leaf *trieLeaf) {
	// 不使用 leaf.parent：事务撤销可能让该指针悬空（同一前缀曾被收缩再重建）。
	// 从根沿键重新定位，深度恒为 128，复杂度仍与集合规模无关。
	var path [trieDepth + 1]*trieNode
	path[0] = s.root
	node := s.root
	for d := 0; d < trieDepth; d++ {
		b := bitAt(leaf.keyHi, leaf.keyLo, d)
		node = node.children[b]
		if node == nil {
			return // 叶子已不在集合中（幂等删除）
		}
		path[d+1] = node
	}
	if node.leaf != leaf {
		return // 键被其他叶子占用或为空：不做任何事
	}
	node.leaf = nil
	leaf.parent = nil
	// 自底向上收缩不再被任何叶子使用的内部节点。
	for i := trieDepth; i >= 1; i-- {
		cur := path[i]
		if cur.leaf != nil ||
			cur.children[0] != nil || cur.children[1] != nil {
			break
		}
		path[i-1].children[cur.bit] = nil
	}
}

// leftmost 返回以 node 为根的子树中键最小的叶子。
func leftmost(node *trieNode) *trieLeaf {
	for node.leaf == nil {
		if node.children[0] != nil {
			node = node.children[0]
		} else {
			node = node.children[1]
		}
	}
	return node.leaf
}

// ascendFirst 从 node 向祖先回溯，返回沿路径第一次可向右拐后子树的最小叶子。
func ascendFirst(node *trieNode) *trieLeaf {
	for node.parent != nil {
		if node.bit == 0 && node.parent.children[1] != nil {
			return leftmost(node.parent.children[1])
		}
		node = node.parent
	}
	return nil
}

// seek 返回首个键 >= (hi,lo) 的叶子；不存在时返回 nil。
func (s *orderedSet) seek(hi, lo uint64) *trieLeaf {
	node := s.root
	for d := 0; d < trieDepth; d++ {
		b := bitAt(hi, lo, d)
		next := node.children[b]
		if next == nil {
			if b == 1 {
				// 目标前缀更大：只能向上找向右拐的祖先。
				return ascendFirst(node)
			}
			// 缺失 0 分支：右兄弟子树全部大于目标键。
			if node.children[1] != nil {
				return leftmost(node.children[1])
			}
			return ascendFirst(node)
		}
		node = next
	}
	if node.leaf != nil {
		return node.leaf
	}
	return ascendFirst(node)
}

// next 返回严格大于 leaf 的最小叶子。
func (s *orderedSet) next(leaf *trieLeaf) *trieLeaf {
	// 与 delete 同理，不依赖可能已被收缩的 parent 指针：按后继键重新 seek。
	lo := leaf.keyLo + 1
	hi := leaf.keyHi
	if lo == 0 { // 低 64 位回绕时进位到高位
		hi++
	}
	if hi < leaf.keyHi { // 128 位整体溢出：不存在后继
		return nil
	}
	return s.seek(hi, lo)
}

func (s *orderedSet) first() *trieLeaf {
	if s.empty() {
		return nil
	}
	return leftmost(s.root)
}

func (s *orderedSet) empty() bool {
	return s.root.children[0] == nil && s.root.children[1] == nil
}
