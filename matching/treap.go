package matching

import "math/rand"

// priceTree 是以价位为键的随机化二叉搜索树（treap）。
// 树中每个节点即一个 level；同一价位的全部操作（插入/删除/极值/前后继）
// 都是 O(log L)，L 为簿内不同价位数，与委托总数无关。
type priceTree struct {
	root *level
	rng  *rand.Rand
}

func newPriceTree() *priceTree {
	// 固定种子：相同操作序列重放时 treap 形态也确定（功能结果本与形态无关）。
	return &priceTree{rng: rand.New(rand.NewSource(1))}
}

func rotateRight(t *level) *level {
	x := t.left
	t.left = x.right
	x.right = t
	return x
}

func rotateLeft(t *level) *level {
	x := t.right
	t.right = x.left
	x.left = t
	return x
}

// insert 插入一个新价位节点；调用方保证价位此前不存在。
func (pt *priceTree) insert(lv *level) {
	lv.priority = pt.rng.Uint64()
	lv.left, lv.right = nil, nil
	pt.root = pt.insertAt(pt.root, lv)
}

func (pt *priceTree) insertAt(t, lv *level) *level {
	if t == nil {
		return lv
	}
	if lv.price < t.price {
		t.left = pt.insertAt(t.left, lv)
		if t.left.priority > t.priority {
			t = rotateRight(t)
		}
	} else {
		t.right = pt.insertAt(t.right, lv)
		if t.right.priority > t.priority {
			t = rotateLeft(t)
		}
	}
	return t
}

// get 取价位节点；不存在返回 nil。
func (pt *priceTree) get(price int64) *level {
	t := pt.root
	for t != nil {
		switch {
		case price < t.price:
			t = t.left
		case price > t.price:
			t = t.right
		default:
			return t
		}
	}
	return nil
}

// erase 删除价位节点；调用方通常仅在 level 已空时删除。
func (pt *priceTree) erase(price int64) {
	pt.root = pt.eraseAt(pt.root, price)
}

func (pt *priceTree) eraseAt(t *level, price int64) *level {
	if t == nil {
		return nil
	}
	switch {
	case price < t.price:
		t.left = pt.eraseAt(t.left, price)
	case price > t.price:
		t.right = pt.eraseAt(t.right, price)
	default:
		if t.left == nil {
			return t.right
		}
		if t.right == nil {
			return t.left
		}
		if t.left.priority > t.right.priority {
			t = rotateRight(t)
			t.right = pt.eraseAt(t.right, price)
		} else {
			t = rotateLeft(t)
			t.left = pt.eraseAt(t.left, price)
		}
	}
	return t
}

// min / max 返回整棵树的极值价位。
func (pt *priceTree) min() *level {
	t := pt.root
	if t == nil {
		return nil
	}
	for t.left != nil {
		t = t.left
	}
	return t
}

func (pt *priceTree) max() *level {
	t := pt.root
	if t == nil {
		return nil
	}
	for t.right != nil {
		t = t.right
	}
	return t
}

// succ 返回严格大于 price 的最小价位；不存在返回 nil。O(log L)。
func (pt *priceTree) succ(price int64) *level {
	var best *level
	t := pt.root
	for t != nil {
		if t.price > price {
			best = t
			t = t.left
		} else {
			t = t.right
		}
	}
	return best
}

// pred 返回严格小于 price 的最大价位；不存在返回 nil。O(log L)。
func (pt *priceTree) pred(price int64) *level {
	var best *level
	t := pt.root
	for t != nil {
		if t.price < price {
			best = t
			t = t.right
		} else {
			t = t.left
		}
	}
	return best
}

// inOrder 以价格升序访问全部价位。
func (pt *priceTree) inOrder(fn func(*level)) {
	var walk func(*level)
	walk = func(t *level) {
		if t == nil {
			return
		}
		walk(t.left)
		fn(t)
		walk(t.right)
	}
	walk(pt.root)
}
