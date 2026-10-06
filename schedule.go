package parking

import "strconv"

type ownershipNode struct {
	tag      Owner
	has      bool
	children [2]*ownershipNode
}

type ownershipTree struct {
	root *ownershipNode
}

func newOwnershipTree() *ownershipTree {
	return &ownershipTree{root: &ownershipNode{}}
}

func spotKey(zone string, number int) string {
	return zone + "#" + strconv.Itoa(number)
}

func (tree *ownershipTree) assign(lo, hi, qlo, qhi uint64, node *ownershipNode, owner Owner) {
	if qlo <= lo && hi <= qhi {
		node.tag = owner
		node.has = true
		node.children = [2]*ownershipNode{}
		return
	}
	mid := lo + (hi-lo)/2
	if node.has {
		if node.children[0] == nil {
			node.children[0] = &ownershipNode{tag: node.tag, has: true}
		}
		if node.children[1] == nil {
			node.children[1] = &ownershipNode{tag: node.tag, has: true}
		}
		node.has = false
		node.tag = Owner{}
	}
	if qlo < mid {
		if node.children[0] == nil {
			node.children[0] = &ownershipNode{}
		}
		tree.assign(lo, mid, qlo, qhi, node.children[0], owner)
	}
	if qhi > mid {
		if node.children[1] == nil {
			node.children[1] = &ownershipNode{}
		}
		tree.assign(mid, hi, qlo, qhi, node.children[1], owner)
	}
}

func (tree *ownershipTree) ownerAt(lo, hi, point uint64, node *ownershipNode) Owner {
	if node == nil {
		return Owner{}
	}
	if node.has {
		return node.tag
	}
	mid := lo + (hi-lo)/2
	if point < mid {
		return tree.ownerAt(lo, mid, point, node.children[0])
	}
	return tree.ownerAt(mid, hi, point, node.children[1])
}

func (l *Lot) assignOwner(zoneName string, number int, from, to int64, owner Owner) {
	if from >= to {
		return
	}
	tree := l.owners[spotKey(zoneName, number)]
	tree.assign(0, 1<<63, uint64(from), uint64(to), tree.root, owner)
}

func (l *Lot) ownerAt(zoneName string, number int, at int64) Owner {
	tree := l.owners[spotKey(zoneName, number)]
	return tree.ownerAt(0, 1<<63, uint64(at), tree.root)
}

func (tree *ownershipTree) freeRange(lo, hi, qlo, qhi uint64, node *ownershipNode) bool {
	if node == nil || !node.has && node.children[0] == nil && node.children[1] == nil {
		return true
	}
	if node.has && node.tag != (Owner{}) {
		return false
	}
	mid := lo + (hi-lo)/2
	if qlo < mid && !tree.freeRange(lo, mid, qlo, minUint(qhi, mid), node.children[0]) {
		return false
	}
	if qhi > mid && !tree.freeRange(mid, hi, maxUint(qlo, mid), qhi, node.children[1]) {
		return false
	}
	return true
}

func minUint(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxUint(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func (l *Lot) rangeFree(zoneName string, number int, from, to int64) bool {
	tree := l.owners[spotKey(zoneName, number)]
	return tree.freeRange(0, 1<<63, uint64(from), uint64(to), tree.root)
}
