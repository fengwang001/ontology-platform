package speq

import "math/rand/v2"

// 到期索引：按 (expiry, id) 排序的随机平衡二叉树（treap）。
//
// 仅保存「当前可能命中预警」的对象：未报废、未封存、未停用。
// 到期日重算、封存/启封、停用/恢复、报废时做 O(log n) 更新。

type treapNode struct {
	keyExpiry int
	keyID     string
	left      *treapNode
	right     *treapNode
	priority  uint64
}

type treap struct {
	root *treapNode
	size int
	rng  *rand.Rand
}

func newTreap() *treap {
	// 每个 treap 使用独立的高熵 PCG 随机源；
	// 所有访问都在 System 锁内，rng 无需额外同步。
	return &treap{rng: rand.New(rand.NewPCG(0x243F6A8885A308D3, 0x13198A2E03707344))}
}

// randPriority 返回随机堆优先级。
func (t *treap) randPriority() uint64 {
	return t.rng.Uint64()
}

func keyLess(e1 int, id1 string, e2 int, id2 string) bool {
	if e1 != e2 {
		return e1 < e2
	}
	return id1 < id2
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

func treapInsert(n **treapNode, expiry int, id string, priority uint64) bool {
	if *n == nil {
		*n = &treapNode{keyExpiry: expiry, keyID: id, priority: priority}
		return true
	}
	cur := *n
	if cur.keyExpiry == expiry && cur.keyID == id {
		return false
	}
	var inserted bool
	if keyLess(expiry, id, cur.keyExpiry, cur.keyID) {
		inserted = treapInsert(&cur.left, expiry, id, priority)
		if cur.left.priority > cur.priority {
			*n = rotateRight(cur)
		}
	} else {
		inserted = treapInsert(&cur.right, expiry, id, priority)
		if cur.right.priority > cur.priority {
			*n = rotateLeft(cur)
		}
	}
	return inserted
}

func (t *treap) insert(expiry int, id string) {
	if treapInsert(&t.root, expiry, id, t.randPriority()) {
		t.size++
	}
}

func treapDelete(n **treapNode, expiry int, id string) bool {
	cur := *n
	if cur == nil {
		return false
	}
	if cur.keyExpiry == expiry && cur.keyID == id {
		switch {
		case cur.left == nil && cur.right == nil:
			*n = nil
		case cur.left == nil:
			*n = cur.right
		case cur.right == nil:
			*n = cur.left
		default:
			if cur.left.priority > cur.right.priority {
				*n = rotateRight(cur)
				treapDelete(&(*n).right, expiry, id)
			} else {
				*n = rotateLeft(cur)
				treapDelete(&(*n).left, expiry, id)
			}
		}
		return true
	}
	if keyLess(expiry, id, cur.keyExpiry, cur.keyID) {
		return treapDelete(&cur.left, expiry, id)
	}
	return treapDelete(&cur.right, expiry, id)
}

func (t *treap) delete(expiry int, id string) {
	if treapDelete(&t.root, expiry, id) {
		t.size--
	}
}

// scanAsc 按 (expiry, id) 升序遍历满足 loExpiry <= expiry <= hiExpiry 的条目，
// fn 返回 false 时提前停止。
func (t *treap) scanAsc(loExpiry, hiExpiry int, fn func(expiry int, id string) bool) {
	var walk func(n *treapNode) bool
	walk = func(n *treapNode) bool {
		if n == nil {
			return true
		}
		if n.keyExpiry < loExpiry {
			return walk(n.right)
		}
		if n.keyExpiry > hiExpiry {
			return walk(n.left)
		}
		if !walk(n.left) {
			return false
		}
		if !fn(n.keyExpiry, n.keyID) {
			return false
		}
		return walk(n.right)
	}
	walk(t.root)
}

// depth 仅用于测试验证平衡性质（对数级高度）。
func (t *treap) depth() int {
	var d func(n *treapNode) int
	d = func(n *treapNode) int {
		if n == nil {
			return 0
		}
		l := d(n.left)
		r := d(n.right)
		if l > r {
			return l + 1
		}
		return r + 1
	}
	return d(t.root)
}
