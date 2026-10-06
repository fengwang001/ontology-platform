package floorcontrol

import "math/rand/v2"

// handQueue 是按举手次序排列、支持按下标 O(期望 log n)
// 定位/删除的有序结构（随机 treap，键为单调递增票号）。
type handQueue struct {
	root *qnode
	byID map[string]*qnode
}

type qnode struct {
	user   string
	ticket int64
	prio   uint64
	left   *qnode
	right  *qnode
	parent *qnode
	size   int
}

func newHandQueue() *handQueue {
	return &handQueue{byID: make(map[string]*qnode)}
}

// 所有操作的期望时间复杂度为 O(log n)；contains 为 O(1)。

func nodeSize(n *qnode) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *qnode) pull() {
	n.size = 1 + nodeSize(n.left) + nodeSize(n.right)
	if n.left != nil {
		n.left.parent = n
	}
	if n.right != nil {
		n.right.parent = n
	}
}

// split 按票号 < t 拆分。
func split(n *qnode, t int64) (*qnode, *qnode) {
	if n == nil {
		return nil, nil
	}
	if n.ticket < t {
		a, b := split(n.right, t)
		n.right = a
		if a != nil {
			a.parent = n
		}
		n.pull()
		n.parent = nil
		return n, b
	}
	a, b := split(n.left, t)
	n.left = b
	if b != nil {
		b.parent = n
	}
	n.pull()
	n.parent = nil
	return a, n
}

// merge 要求 a 中所有票号小于 b 中所有票号。
func merge(a, b *qnode) *qnode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = merge(a.right, b)
		a.pull()
		a.parent = nil
		return a
	}
	b.left = merge(a, b.left)
	b.pull()
	b.parent = nil
	return b
}

func (q *handQueue) len() int { return nodeSize(q.root) }

func (q *handQueue) push(user string, ticket int64) {
	n := &qnode{user: user, ticket: ticket, prio: rand.Uint64(), size: 1}
	q.byID[user] = n
	q.root = merge(q.root, n)
}

func (q *handQueue) remove(user string) bool {
	n, ok := q.byID[user]
	if !ok {
		return false
	}
	delete(q.byID, user)
	a, c := split(q.root, n.ticket)
	_, c = split(c, n.ticket+1)
	q.root = merge(a, c)
	return true
}

// popHead 取出票号最小（最早举手）的成员。
func (q *handQueue) popHead() (string, bool) {
	n := q.root
	if n == nil {
		return "", false
	}
	for n.left != nil {
		n = n.left
	}
	user := n.user
	q.remove(user)
	return user, true
}

func (q *handQueue) head() (string, bool) {
	n := q.root
	if n == nil {
		return "", false
	}
	for n.left != nil {
		n = n.left
	}
	return n.user, true
}

func (q *handQueue) contains(user string) bool {
	_, ok := q.byID[user]
	return ok
}

// position 返回 1 基名次；不存在时 ok 为 false。
func (q *handQueue) position(user string) (int, bool) {
	n, ok := q.byID[user]
	if !ok {
		return 0, false
	}
	pos := nodeSize(n.left) + 1
	for n.parent != nil {
		if n == n.parent.right {
			pos += nodeSize(n.parent.left) + 1
		}
		n = n.parent
	}
	return pos, true
}

func (q *handQueue) ordered() []string {
	out := make([]string, 0, q.len())
	var walk func(*qnode)
	walk = func(n *qnode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.user)
		walk(n.right)
	}
	walk(q.root)
	return out
}
