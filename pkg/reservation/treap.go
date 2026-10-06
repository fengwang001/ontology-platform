package reservation

// eventTreap stores, for each integer time t, the net occupancy delta at t
// (starts add, ends subtract). Subtree sum is maintained so the sum of deltas
// at times strictly before t can be read in O(log n); that prefix sum is the
// occupancy over the interval of interest at time t.
type eventNode struct {
	key      int
	delta    int
	sum      int
	priority uint64
	left     *eventNode
	right    *eventNode
}

type eventTreap struct {
	root *eventNode
	rng  uint64
}

func newEventTreap() *eventTreap {
	return &eventTreap{rng: 0x9e3779b97f4a7c15}
}

func (t *eventTreap) nextPriority() uint64 {
	x := t.rng
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	t.rng = x
	return x * 0x2545F4914F6CDD1D
}

func eventSum(n *eventNode) int {
	if n == nil {
		return 0
	}
	return n.sum
}

func eventPull(n *eventNode) {
	n.sum = eventSum(n.left) + n.delta + eventSum(n.right)
}

func eventRotateRight(n *eventNode) *eventNode {
	l := n.left
	n.left = l.right
	l.right = n
	eventPull(n)
	eventPull(l)
	return l
}

func eventRotateLeft(n *eventNode) *eventNode {
	r := n.right
	n.right = r.left
	r.left = n
	eventPull(n)
	eventPull(r)
	return r
}

func (t *eventTreap) insert(n *eventNode, key, delta int) *eventNode {
	if n == nil {
		return &eventNode{key: key, delta: delta, sum: delta, priority: t.nextPriority()}
	}
	switch {
	case key < n.key:
		n.left = t.insert(n.left, key, delta)
		if n.left.priority > n.priority {
			n = eventRotateRight(n)
		}
	case key > n.key:
		n.right = t.insert(n.right, key, delta)
		if n.right.priority > n.priority {
			n = eventRotateLeft(n)
		}
	default:
		n.delta += delta
	}
	eventPull(n)
	return n
}

// add applies delta at tm. A node whose delta becomes zero is deleted.
func (t *eventTreap) add(tm, delta int) { t.root = t.insert(t.root, tm, delta) }

func (t *eventTreap) remove(tm, delta int) { t.add(tm, -delta) }

// prefixBefore returns the sum of deltas at times strictly before tm.
func (t *eventTreap) prefixBefore(tm int) int {
	total := 0
	for n := t.root; n != nil; {
		if n.key < tm {
			total += eventSum(n.left) + n.delta
			n = n.right
		} else {
			n = n.left
		}
	}
	return total
}

// firstKeyAtOrAfter returns the smallest stored time >= tm, or false.
func (t *eventTreap) firstKeyAtOrAfter(tm int) (int, bool) {
	var best int
	found := false
	for n := t.root; n != nil; {
		if n.key >= tm {
			best, found = n.key, true
			n = n.left
		} else {
			n = n.right
		}
	}
	return best, found
}

// keysIn appends stored times in [lo, hi) in ascending order.
func (t *eventTreap) keysIn(lo, hi int, out *[]int) {
	if t == nil {
		return
	}
	var walk func(n *eventNode)
	walk = func(n *eventNode) {
		if n == nil || n.key >= hi {
			if n != nil {
				walk(n.left)
			}
			return
		}
		if n.key < lo {
			walk(n.right)
			return
		}
		walk(n.left)
		*out = append(*out, n.key)
		walk(n.right)
	}
	walk(t.root)
}

// expiryTreap orders reservations by (expiresAt, id) so that settling the
// clock only visits holds that actually expire; each node is inserted and
// removed at most once between two settle passes.
type expNode struct {
	expiresAt int
	id        int64
	priority  uint64
	left      *expNode
	right     *expNode
}

type expiryTreap struct {
	root *expNode
	rng  uint64
}

func newExpiryTreap() *expiryTreap {
	return &expiryTreap{rng: 0x123456789abcdef0}
}

func (t *expiryTreap) nextPriority() uint64 {
	x := t.rng
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	t.rng = x
	return x * 0x2545F4914F6CDD1D
}

func expLess(aExp int, aID int64, bExp int, bID int64) bool {
	return aExp < bExp || (aExp == bExp && aID < bID)
}

func (t *expiryTreap) insert(n *expNode, node *expNode) *expNode {
	if n == nil {
		return node
	}
	if expLess(node.expiresAt, node.id, n.expiresAt, n.id) {
		n.left = t.insert(n.left, node)
		if n.left.priority > n.priority {
			l := n.left
			n.left = l.right
			l.right = n
			n = l
		}
	} else {
		n.right = t.insert(n.right, node)
		if n.right.priority > n.priority {
			r := n.right
			n.right = r.left
			r.left = n
			n = r
		}
	}
	return n
}

func (t *expiryTreap) add(expiresAt int, id int64) {
	t.root = t.insert(t.root, &expNode{expiresAt: expiresAt, id: id, priority: t.nextPriority()})
}

func (t *expiryTreap) erase(n *expNode, expiresAt int, id int64) *expNode {
	if n == nil {
		return nil
	}
	switch {
	case expLess(expiresAt, id, n.expiresAt, n.id):
		n.left = t.erase(n.left, expiresAt, id)
	case expLess(n.expiresAt, n.id, expiresAt, id):
		n.right = t.erase(n.right, expiresAt, id)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		if n.left.priority > n.right.priority {
			l := n.left
			n.left = l.right
			l.right = t.erase(n, expiresAt, id)
			n = l
		} else {
			r := n.right
			n.right = r.left
			r.left = t.erase(n, expiresAt, id)
			n = r
		}
	}
	return n
}

func (t *expiryTreap) remove(expiresAt int, id int64) {
	t.root = t.erase(t.root, expiresAt, id)
}

// drain invokes fn for every node with expiresAt <= limit in ascending key
// order and removes it.
func (t *expiryTreap) drain(limit int, fn func(expiresAt int, id int64)) {
	for t.root != nil {
		n := t.root
		for n.left != nil {
			n = n.left
		}
		if n.expiresAt > limit {
			return
		}
		fn(n.expiresAt, n.id)
		t.root = t.erase(t.root, n.expiresAt, n.id)
	}
}
