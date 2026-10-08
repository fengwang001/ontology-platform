package endpointshard

import "sync/atomic"

// sizeKey orders shards by (endpoint count, shard number). The total
// order matches every tie-break rule of the maintainer: "fewest
// endpoints, ties to the lowest number" and "most endpoints but not
// full, ties to the lowest number".
type sizeKey struct {
	size int
	num  int
}

func keyLess(a, b sizeKey) bool {
	if a.size != b.size {
		return a.size < b.size
	}
	return a.num < b.num
}

// treapNode is a node of the deterministic treap backing sizeIndex.
// Priorities are a pure function of the shard number, so the tree
// shape depends only on the set of live keys, never on call order or
// randomness; every operation is fully reproducible.
type treapNode struct {
	key   sizeKey
	prio  uint64
	left  *treapNode
	right *treapNode
}

// sizeIndex keeps every live shard keyed by (size, num) so that
// placement, empty-shard cleanup and merge selection all cost
// O(log S) in the number of shards S, independent of how many shards
// are unaffected by the current call.
type sizeIndex struct {
	root   *treapNode
	visits *atomic.Int64
}

func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

func (x *sizeIndex) visit() {
	if x.visits != nil {
		x.visits.Add(1)
	}
}

func rotateLeft(n *treapNode) *treapNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func rotateRight(n *treapNode) *treapNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func (x *sizeIndex) insert(root *treapNode, key sizeKey) *treapNode {
	if root == nil {
		return &treapNode{key: key, prio: splitmix64(uint64(key.num))}
	}
	x.visit()
	if keyLess(key, root.key) {
		root.left = x.insert(root.left, key)
		if root.left.prio < root.prio {
			root = rotateRight(root)
		}
	} else {
		root.right = x.insert(root.right, key)
		if root.right.prio < root.prio {
			root = rotateLeft(root)
		}
	}
	return root
}

func (x *sizeIndex) remove(root *treapNode, key sizeKey) *treapNode {
	if root == nil {
		return nil
	}
	x.visit()
	switch {
	case keyLess(key, root.key):
		root.left = x.remove(root.left, key)
	case keyLess(root.key, key):
		root.right = x.remove(root.right, key)
	default:
		if root.left == nil {
			return root.right
		}
		if root.right == nil {
			return root.left
		}
		if root.left.prio < root.right.prio {
			root = rotateRight(root)
			root.right = x.remove(root.right, key)
		} else {
			root = rotateLeft(root)
			root.left = x.remove(root.left, key)
		}
	}
	return root
}

func (x *sizeIndex) add(size, num int) { x.root = x.insert(x.root, sizeKey{size: size, num: num}) }
func (x *sizeIndex) del(size, num int) { x.root = x.remove(x.root, sizeKey{size: size, num: num}) }

// min returns the smallest key: fewest endpoints, ties to lowest num.
func (x *sizeIndex) min() (sizeKey, bool) {
	n := x.root
	if n == nil {
		return sizeKey{}, false
	}
	for n.left != nil {
		x.visit()
		n = n.left
	}
	return n.key, true
}

// twoMin returns up to two smallest keys in (size, num) order.
func (x *sizeIndex) twoMin() ([2]sizeKey, int) {
	var res [2]sizeKey
	count := 0
	var stack []*treapNode
	n := x.root
	for n != nil || len(stack) > 0 {
		for n != nil {
			x.visit()
			stack = append(stack, n)
			n = n.left
		}
		n = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		res[count] = n.key
		count++
		if count == 2 {
			return res, count
		}
		n = n.right
	}
	return res, count
}

// maxNonFull returns (maxSize, minNum) among shards with size < m,
// i.e. the placement target: most endpoints but not full, ties to the
// lowest shard number.
func (x *sizeIndex) maxNonFull(m int) (sizeKey, bool) {
	n := x.root
	maxSize := -1
	for n != nil {
		x.visit()
		if n.key.size < m {
			maxSize = n.key.size
			n = n.right
		} else {
			n = n.left
		}
	}
	if maxSize < 0 {
		return sizeKey{}, false
	}
	n = x.root
	best := -1
	for n != nil {
		x.visit()
		switch {
		case n.key.size == maxSize:
			best = n.key.num
			n = n.left
		case n.key.size < maxSize:
			n = n.right
		default:
			n = n.left
		}
	}
	return sizeKey{size: maxSize, num: best}, true
}

// collectSizeGT returns every key with size > m, in ascending order.
// Used by Resize to find over-capacity shards without scanning
// unaffected shards.
func (x *sizeIndex) collectSizeGT(m int) []sizeKey {
	var out []sizeKey
	var walk func(n *treapNode)
	walk = func(n *treapNode) {
		if n == nil {
			return
		}
		x.visit()
		if n.key.size > m {
			walk(n.left)
			out = append(out, n.key)
			walk(n.right)
		} else {
			walk(n.right)
		}
	}
	walk(x.root)
	return out
}
