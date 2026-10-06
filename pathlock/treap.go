package pathlock

import "math/rand/v2"

// treapNode 是按路径字节序排序的持久化（不可变）树节点。
// 每次写操作只复制从根到变更点的 O(log n) 个节点，旧版本根天然可作为快照复用。
type treapNode struct {
	key   string
	prio  uint64
	lock  Lock
	left  *treapNode
	right *treapNode
}

// treapInsert 返回包含 l（按其 Path 索引）的新树根；同键覆盖旧值。
func treapInsert(root *treapNode, l Lock) *treapNode {
	if root == nil {
		return &treapNode{key: l.Path, prio: rand.Uint64(), lock: l}
	}
	if l.Path == root.key {
		n := *root
		n.lock = l
		return &n
	}
	if l.Path < root.key {
		n := *root
		n.left = treapInsert(root.left, l)
		if n.left.prio > n.prio {
			return rotateRight(&n)
		}
		return &n
	}
	n := *root
	n.right = treapInsert(root.right, l)
	if n.right.prio > n.prio {
		return rotateLeft(&n)
	}
	return &n
}

func rotateRight(n *treapNode) *treapNode {
	x := n.left
	nn := *n
	nn.left = x.right
	xn := *x
	xn.right = &nn
	return &xn
}

func rotateLeft(n *treapNode) *treapNode {
	x := n.right
	nn := *n
	nn.right = x.left
	xn := *x
	xn.left = &nn
	return &xn
}

// treapDelete 返回删除 key 后的新树根；键不存在时原样返回。
func treapDelete(root *treapNode, key string) *treapNode {
	if root == nil {
		return nil
	}
	if key < root.key {
		n := *root
		n.left = treapDelete(root.left, key)
		return &n
	}
	if key > root.key {
		n := *root
		n.right = treapDelete(root.right, key)
		return &n
	}
	return treapMerge(root.left, root.right)
}

func treapMerge(a, b *treapNode) *treapNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		n := *a
		n.right = treapMerge(a.right, b)
		return &n
	}
	n := *b
	n.left = treapMerge(a, b.left)
	return &n
}

func treapGet(root *treapNode, key string) (Lock, bool) {
	for root != nil {
		switch {
		case key == root.key:
			return root.lock, true
		case key < root.key:
			root = root.left
		default:
			root = root.right
		}
	}
	return Lock{}, false
}

// treapAscend 按键升序遍历 [start, +∞)；fn 返回 false 即停止。
func treapAscend(root *treapNode, start string, fn func(Lock) bool) {
	var walk func(n *treapNode) bool
	walk = func(n *treapNode) bool {
		if n == nil {
			return true
		}
		if start <= n.key {
			if !walk(n.left) {
				return false
			}
			if !fn(n.lock) {
				return false
			}
		}
		return walk(n.right)
	}
	walk(root)
}

// underPrefix 判断 key 是否等于 prefix 或位于 prefix/ 目录下。
func underPrefix(key, prefix string) bool {
	return key == prefix ||
		(len(key) > len(prefix) &&
			key[:len(prefix)] == prefix &&
			key[len(prefix)] == '/')
}
