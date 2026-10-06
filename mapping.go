package thinpool

type blockMapping struct {
	root *mappingNode
}

type mappingNode struct {
	virtualBlock  uint64
	physicalBlock uint64
	left          *mappingNode
	right         *mappingNode
	height        int
	size          uint64
}

func newBlockMapping() *blockMapping {
	return &blockMapping{}
}

func (m *blockMapping) lookup(virtualBlock uint64) (uint64, bool) {
	return m.root.lookup(virtualBlock)
}

func (m *blockMapping) insert(virtualBlock, physicalBlock uint64) {
	m.root = m.root.insert(virtualBlock, physicalBlock)
}

func (m *blockMapping) deleteRange(start, end uint64) []uint64 {
	if start >= end {
		return nil
	}

	mapped := m.root.collectRange(start, end, nil)
	freed := make([]uint64, 0, len(mapped))
	for _, node := range mapped {
		freed = append(freed, node.physicalBlock)
		m.root = m.root.delete(node.virtualBlock)
	}
	return freed
}

func (m *blockMapping) countRange(start, end uint64) uint64 {
	if start >= end {
		return 0
	}
	return m.root.countRange(start, end)
}

func (m *blockMapping) snapshot() map[uint64]uint64 {
	result := make(map[uint64]uint64, m.root.nodeSize())
	m.root.appendAll(result)
	return result
}

func (n *mappingNode) lookup(virtualBlock uint64) (uint64, bool) {
	for n != nil {
		switch {
		case virtualBlock < n.virtualBlock:
			n = n.left
		case virtualBlock > n.virtualBlock:
			n = n.right
		default:
			return n.physicalBlock, true
		}
	}
	return 0, false
}

func (n *mappingNode) insert(virtualBlock, physicalBlock uint64) *mappingNode {
	if n == nil {
		return &mappingNode{
			virtualBlock:  virtualBlock,
			physicalBlock: physicalBlock,
			height:        1,
			size:          1,
		}
	}

	if virtualBlock < n.virtualBlock {
		n.left = n.left.insert(virtualBlock, physicalBlock)
	} else if virtualBlock > n.virtualBlock {
		n.right = n.right.insert(virtualBlock, physicalBlock)
	} else {
		n.physicalBlock = physicalBlock
		return n
	}
	return n.balance()
}

func (n *mappingNode) delete(virtualBlock uint64) *mappingNode {
	if n == nil {
		return nil
	}

	switch {
	case virtualBlock < n.virtualBlock:
		n.left = n.left.delete(virtualBlock)
	case virtualBlock > n.virtualBlock:
		n.right = n.right.delete(virtualBlock)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		successor := n.right.minimum()
		n.virtualBlock = successor.virtualBlock
		n.physicalBlock = successor.physicalBlock
		n.right = n.right.delete(successor.virtualBlock)
	}
	return n.balance()
}

func (n *mappingNode) collectRange(start, end uint64, result []*mappingNode) []*mappingNode {
	if n == nil {
		return result
	}
	if n.virtualBlock >= end {
		return n.left.collectRange(start, end, result)
	}
	if n.virtualBlock < start {
		return n.right.collectRange(start, end, result)
	}
	result = n.left.collectRange(start, end, result)
	result = append(result, n)
	return n.right.collectRange(start, end, result)
}

func (n *mappingNode) countRange(start, end uint64) uint64 {
	if n == nil {
		return 0
	}
	if n.virtualBlock >= end {
		return n.left.countRange(start, end)
	}
	if n.virtualBlock < start {
		return n.right.countRange(start, end)
	}
	return n.left.countFrom(start) + 1 + n.right.countUntil(end)
}

func (n *mappingNode) countFrom(start uint64) uint64 {
	var count uint64
	for n != nil {
		if n.virtualBlock >= start {
			count += n.right.nodeSize() + 1
			n = n.left
		} else {
			n = n.right
		}
	}
	return count
}

func (n *mappingNode) countUntil(end uint64) uint64 {
	var count uint64
	for n != nil {
		if n.virtualBlock < end {
			count += n.left.nodeSize() + 1
			n = n.right
		} else {
			n = n.left
		}
	}
	return count
}

func (n *mappingNode) minimum() *mappingNode {
	for n.left != nil {
		n = n.left
	}
	return n
}

func (n *mappingNode) balance() *mappingNode {
	n.refresh()
	balanceFactor := n.left.nodeHeight() - n.right.nodeHeight()
	if balanceFactor > 1 {
		if n.left.left.nodeHeight() < n.left.right.nodeHeight() {
			n.left = n.left.rotateLeft()
		}
		return n.rotateRight()
	}
	if balanceFactor < -1 {
		if n.right.right.nodeHeight() < n.right.left.nodeHeight() {
			n.right = n.right.rotateRight()
		}
		return n.rotateLeft()
	}
	return n
}

func (n *mappingNode) rotateRight() *mappingNode {
	pivot := n.left
	n.left = pivot.right
	pivot.right = n
	n.refresh()
	pivot.refresh()
	return pivot
}

func (n *mappingNode) rotateLeft() *mappingNode {
	pivot := n.right
	n.right = pivot.left
	pivot.left = n
	n.refresh()
	pivot.refresh()
	return pivot
}

func (n *mappingNode) refresh() {
	n.height = 1 + max(n.left.nodeHeight(), n.right.nodeHeight())
	n.size = 1 + n.left.nodeSize() + n.right.nodeSize()
}

func (n *mappingNode) nodeHeight() int {
	if n == nil {
		return 0
	}
	return n.height
}

func (n *mappingNode) nodeSize() uint64 {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *mappingNode) appendAll(result map[uint64]uint64) {
	if n == nil {
		return
	}
	n.left.appendAll(result)
	result[n.virtualBlock] = n.physicalBlock
	n.right.appendAll(result)
}
