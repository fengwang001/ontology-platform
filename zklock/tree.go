package zklock

// avlNode 是以节点序号为键的 AVL 树节点。
type avlNode struct {
	key    int
	val    *child
	left   *avlNode
	right  *avlNode
	height int
}

func h(t *avlNode) int {
	if t == nil {
		return 0
	}
	return t.height
}

func rotateRight(t *avlNode) *avlNode {
	x := t.left
	t.left = x.right
	x.right = t
	t.height = 1 + max(h(t.left), h(t.right))
	x.height = 1 + max(h(x.left), x.right.height)
	return x
}

func rotateLeft(t *avlNode) *avlNode {
	x := t.right
	t.right = x.left
	x.left = t
	t.height = 1 + max(h(t.left), h(t.right))
	x.height = 1 + max(x.left.height, h(x.right))
	return x
}

func balance(t *avlNode) *avlNode {
	bf := h(t.left) - h(t.right)
	if bf > 1 {
		if h(t.left.left) < h(t.left.right) {
			t.left = rotateLeft(t.left)
		}
		return rotateRight(t)
	}
	if bf < -1 {
		if h(t.right.right) < h(t.right.left) {
			t.right = rotateRight(t.right)
		}
		return rotateLeft(t)
	}
	return t
}

func avlInsert(t *avlNode, key int, val *child) *avlNode {
	if t == nil {
		return &avlNode{key: key, val: val, height: 1}
	}
	if key < t.key {
		t.left = avlInsert(t.left, key, val)
	} else {
		t.right = avlInsert(t.right, key, val)
	}
	t.height = 1 + max(h(t.left), h(t.right))
	return balance(t)
}

func avlDelete(t *avlNode, key int) *avlNode {
	if t == nil {
		return nil
	}
	if key < t.key {
		t.left = avlDelete(t.left, key)
	} else if key > t.key {
		t.right = avlDelete(t.right, key)
	} else {
		if t.left == nil {
			return t.right
		}
		if t.right == nil {
			return t.left
		}
		s := t.right
		for s.left != nil {
			s = s.left
		}
		t.key = s.key
		t.val = s.val
		t.right = avlDelete(t.right, s.key)
	}
	t.height = 1 + max(h(t.left), h(t.right))
	return balance(t)
}

func avlGet(t *avlNode, key int) *avlNode {
	for t != nil {
		if key == t.key {
			return t
		}
		if key < t.key {
			t = t.left
		} else {
			t = t.right
		}
	}
	return nil
}

// avlMaxBelow 返回序号严格小于 key 的最大键节点。
func avlMaxBelow(t *avlNode, key int) *avlNode {
	var best *avlNode
	for t != nil {
		if t.key < key {
			best = t
			t = t.right
		} else {
			t = t.left
		}
	}
	return best
}

// avlMin 返回最小键节点。
func avlMin(t *avlNode) *avlNode {
	for t != nil && t.left != nil {
		t = t.left
	}
	return t
}

// avlInorder 按键升序收集节点。
func avlInorder(t *avlNode, out []*child) []*child {
	if t == nil {
		return out
	}
	out = avlInorder(t.left, out)
	out = append(out, t.val)
	out = avlInorder(t.right, out)
	return out
}
