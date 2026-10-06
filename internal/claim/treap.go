package claim

import "sync/atomic"

// treapNode is one policy inside an augmented treap.
//
// Keys are ordered by (StartDay, PolicyID). Every node caches the largest
// EndDay in its subtree (maxEnd), which lets a point query skip whole
// subtrees whose policies all expire before the queried day.
type treapNode struct {
	policyID  string
	startDay  int
	endDay    int
	cancelDay int
	priority  uint32
	left      *treapNode
	right     *treapNode
	maxEnd    int
	size      int
}

type treap struct {
	root    *treapNode
	counter atomic.Uint64
	touched int
}

func newTreap() *treap { return &treap{} }

// priority is deterministic: a fixed-mix hash of a global insertion counter.
// Determinism is what makes identical operation sequences replay identically.
func (t *treap) nextPriority() uint32 {
	x := t.counter.Add(1)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return uint32(x ^ (x >> 32))
}

func treapLess(startDay int, id string, o *treapNode) bool {
	if startDay != o.startDay {
		return startDay < o.startDay
	}
	return id < o.policyID
}

func recompute(n *treapNode) {
	n.maxEnd = n.endDay
	n.size = 1
	if n.left != nil {
		n.size += n.left.size
		if n.left.maxEnd > n.maxEnd {
			n.maxEnd = n.left.maxEnd
		}
	}
	if n.right != nil {
		n.size += n.right.size
		if n.right.maxEnd > n.maxEnd {
			n.maxEnd = n.right.maxEnd
		}
	}
}

func rotateRight(n *treapNode) *treapNode {
	left := n.left
	n.left = left.right
	left.right = n
	recompute(n)
	recompute(left)
	return left
}

func rotateLeft(n *treapNode) *treapNode {
	right := n.right
	n.right = right.left
	right.left = n
	recompute(n)
	recompute(right)
	return right
}

func treapInsert(n *treapNode, ins *treapNode) *treapNode {
	if n == nil {
		recompute(ins)
		return ins
	}
	if treapLess(ins.startDay, ins.policyID, n) {
		n.left = treapInsert(n.left, ins)
		if n.left.priority > n.priority {
			n = rotateRight(n)
		}
	} else {
		n.right = treapInsert(n.right, ins)
		if n.right.priority > n.priority {
			n = rotateLeft(n)
		}
	}
	recompute(n)
	return n
}

func (t *treap) insert(policyID string, startDay, endDay int) {
	node := &treapNode{
		policyID:  policyID,
		startDay:  startDay,
		endDay:    endDay,
		cancelDay: int(^uint(0) >> 1),
		priority:  t.nextPriority(),
	}
	t.root = treapInsert(t.root, node)
}

func (t *treap) setCancel(startDay int, policyID string, cancelDay int) {
	t.root = setCancelDay(t.root, startDay, policyID, cancelDay)
}

func setCancelDay(n *treapNode, startDay int, id string, cancelDay int) *treapNode {
	if n == nil {
		return nil
	}
	if n.startDay == startDay && n.policyID == id {
		n.cancelDay = cancelDay
		return n
	}
	if treapLess(startDay, id, n) {
		n.left = setCancelDay(n.left, startDay, id, cancelDay)
	} else {
		n.right = setCancelDay(n.right, startDay, id, cancelDay)
	}
	return n
}

func treapErase(n *treapNode, startDay int, id string) *treapNode {
	if n == nil {
		return nil
	}
	if startDay == n.startDay && id == n.policyID {
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		default:
			if n.left.priority > n.right.priority {
				n = rotateRight(n)
				n.right = treapErase(n.right, startDay, id)
			} else {
				n = rotateLeft(n)
				n.left = treapErase(n.left, startDay, id)
			}
		}
	} else if treapLess(startDay, id, n) {
		n.left = treapErase(n.left, startDay, id)
	} else {
		n.right = treapErase(n.right, startDay, id)
	}
	recompute(n)
	return n
}

func (t *treap) erase(startDay int, policyID string) {
	t.root = treapErase(t.root, startDay, policyID)
}

// coveringAt appends the ids of policies covering an accident on day.
//
// A subtree is pruned without descending when:
//   - its smallest start day (in-order successor) is already past day, or
//   - every policy in it expires before day (maxEnd < day).
//
// Cancellation takes effect on its day for new registrations, so a node is
// reported only when cancelDay > day. Cancelled nodes stay in the tree: they
// may still cover accidents dated strictly before the cancellation day.
func (t *treap) coveringAt(day int, dst []string) []string {
	t.touched = 0
	return t.collect(t.root, day, dst)
}

func (t *treap) collect(n *treapNode, day int, dst []string) []string {
	if n == nil {
		return dst
	}
	t.touched++
	// No policy in this subtree starts at or before day when the left
	// spine is fully past day: because keys are (startDay, id), if this
	// node starts after day, its right subtree starts even later.
	if n.startDay > day {
		return t.collect(n.left, day, dst)
	}
	// n.startDay <= day, so policies can live in both subtrees, but a
	// whole subtree may expire before day.
	if n.left == nil || n.left.maxEnd >= day {
		dst = t.collect(n.left, day, dst)
	}
	if n.endDay >= day && n.cancelDay > day {
		dst = append(dst, n.policyID)
	}
	if n.right == nil || n.right.maxEnd >= day {
		dst = t.collect(n.right, day, dst)
	}
	return dst
}

// touchedNodes returns how many nodes the last coveringAt call inspected.
func (t *treap) touchedNodes() int { return t.touched }

func (t *treap) size() int {
	if t.root == nil {
		return 0
	}
	return t.root.size
}
