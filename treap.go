package ontology

func bucketPriority(ts int64) uint64 {
	x := uint64(ts) + 0x9e3779b97f4a7c15
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	if x == 0 {
		return 1
	}
	return x
}

func treapSplit(root *treapNode, ts int64) (*treapNode, *treapNode) {
	if root == nil {
		return nil, nil
	}
	if root.ts <= ts {
		left, right := treapSplit(root.right, ts)
		root.right = left
		return root, right
	}
	left, right := treapSplit(root.left, ts)
	root.left = right
	return left, root
}

func treapMerge(left *treapNode, right *treapNode) *treapNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority > right.priority {
		left.right = treapMerge(left.right, right)
		return left
	}
	right.left = treapMerge(left, right.left)
	return right
}

func treapInsert(root *treapNode, node *treapNode) *treapNode {
	left, right := treapSplit(root, node.ts)
	return treapMerge(treapMerge(left, node), right)
}

func treapDelete(root *treapNode, ts int64) *treapNode {
	if root == nil {
		return nil
	}
	if root.ts == ts {
		return treapMerge(root.left, root.right)
	}
	if ts < root.ts {
		root.left = treapDelete(root.left, ts)
	} else {
		root.right = treapDelete(root.right, ts)
	}
	return root
}

func treapRange(root *treapNode, low int64, high int64, visit func(*treapNode) bool) bool {
	if root == nil {
		return true
	}
	left, rest := treapSplit(root, low-1)
	middle, right := treapSplit(rest, high)
	ok := treapVisitInOrder(middle, visit)
	*root = *treapMerge(treapMerge(left, middle), right)
	return ok
}

func treapVisitInOrder(root *treapNode, visit func(*treapNode) bool) bool {
	if root == nil {
		return true
	}
	return treapVisitInOrder(root.left, visit) && visit(root) && treapVisitInOrder(root.right, visit)
}

func treapFirst(root *treapNode) *treapNode {
	for root.left != nil {
		root = root.left
	}
	return root
}
