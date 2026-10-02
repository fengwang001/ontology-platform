package mvgc

// treapNode 是按 key 字节序组织的最小堆 treap 节点。
// 优先级仅用于结构平衡，不影响任何可观察语义。
type treapNode struct {
	key   string
	recs  []rec
	prio  uint64
	left  *treapNode
	right *treapNode
}

func rotateRight(n *treapNode) *treapNode {
	child := n.left
	n.left = child.right
	child.right = n
	return child
}

func rotateLeft(n *treapNode) *treapNode {
	child := n.right
	n.right = child.left
	child.left = n
	return child
}

// treapInsert 插入键；若键已存在则返回原节点与 false。
func treapInsert(root *treapNode, key string, prio uint64) (*treapNode, *treapNode, bool) {
	if root == nil {
		n := &treapNode{key: key, prio: prio}
		return n, n, true
	}
	if key == root.key {
		return root, root, false
	}
	var node *treapNode
	var inserted bool
	if key < root.key {
		var nl *treapNode
		nl, node, inserted = treapInsert(root.left, key, prio)
		root.left = nl
		if root.left.prio < root.prio {
			root = rotateRight(root)
		}
	} else {
		var nr *treapNode
		nr, node, inserted = treapInsert(root.right, key, prio)
		root.right = nr
		if root.right.prio < root.prio {
			root = rotateLeft(root)
		}
	}
	return root, node, inserted
}

func treapFind(root *treapNode, key string) *treapNode {
	for root != nil {
		switch {
		case key == root.key:
			return root
		case key < root.key:
			root = root.left
		default:
			root = root.right
		}
	}
	return nil
}

func treapDelete(root *treapNode, key string) *treapNode {
	if root == nil {
		return nil
	}
	switch {
	case key < root.key:
		root.left = treapDelete(root.left, key)
	case key > root.key:
		root.right = treapDelete(root.right, key)
	default:
		if root.left == nil {
			return root.right
		}
		if root.right == nil {
			return root.left
		}
		if root.left.prio < root.right.prio {
			root = rotateRight(root)
			root.right = treapDelete(root.right, key)
		} else {
			root = rotateLeft(root)
			root.left = treapDelete(root.left, key)
		}
	}
	return root
}

// treapCollectAfter 收集严格大于 cursor 的最小至多 n 个键节点，按键序升序。
func treapCollectAfter(root *treapNode, cursor string, n int, out *[]*treapNode) {
	if root == nil || len(*out) >= n {
		return
	}
	if root.key > cursor {
		treapCollectAfter(root.left, cursor, n, out)
		if len(*out) >= n {
			return
		}
		if root.key > cursor {
			*out = append(*out, root)
		}
		if len(*out) < n {
			treapCollectAfter(root.right, cursor, n, out)
		}
	} else {
		treapCollectAfter(root.right, cursor, n, out)
	}
}

// treapInorder 按键序收集全部节点。
func treapInorder(root *treapNode, out *[]*treapNode) {
	if root == nil {
		return
	}
	treapInorder(root.left, out)
	*out = append(*out, root)
	treapInorder(root.right, out)
}
