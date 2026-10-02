package ontology

type blockNode struct {
	start, end  int64
	priority    uint64
	left, right *blockNode
	lazy        int
	count       int
	size        int64
	firstStart  int64
	lastEnd     int64
	stats       [3]runStats
}

type runStats struct {
	prefix, suffix, max int64
}

type blockTree struct {
	root         *blockNode
	nextPriority uint64
	visited      uint64
}

func newBlockTree(low, high int64) *blockTree {
	t := &blockTree{nextPriority: 0x9e3779b97f4a7c15}
	t.root = t.newRun(low, high, 0)
	return t
}

func (t *blockTree) newRun(start, end int64, count int) *blockNode {
	n := &blockNode{start: start, end: end, count: count, priority: t.priority(), firstStart: start, lastEnd: end}
	t.pull(n)
	return n
}

func (t *blockTree) priority() uint64 {
	x := t.nextPriority
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	t.nextPriority = x
	return x*0x2545f4914f6cdd1d | 1
}

func (t *blockTree) add(low, high, delta int64) {
	left, middleRight := t.split(t.root, low)
	middle, right := t.split(middleRight, high)
	t.apply(middle, int(delta))
	t.root = t.merge(t.merge(left, middle), right)
}

func (t *blockTree) rangeMin(low, high int64) int {
	return t.rangeMinNode(t.root, low, high)
}

func (t *blockTree) hasFreeRun(low, high, length int64) bool {
	if high-low < length {
		return false
	}
	return t.hasFreeRunNode(t.root, low, high, length, 0)
}

func (t *blockTree) hasFreeRunNode(n *blockNode, low, high, need, suffix int64) bool {
	stats, size := t.rangeStats(n, low, high)
	return stats.max >= need && size >= need
}

func (t *blockTree) rangeStats(n *blockNode, low, high int64) (runStats, int64) {
	if n == nil || low >= high || high <= n.firstStart || low >= n.lastEnd {
		return runStats{}, 0
	}
	if low <= n.firstStart && high >= n.lastEnd {
		return n.stats[0], n.size
	}
	t.push(n)
	stats := runStats{}
	size := int64(0)
	leftStats, leftSize := t.rangeStats(n.left, low, high)
	if leftSize > 0 {
		stats, size = leftStats, leftSize
	}
	ownSize := minInt64(n.end, high) - maxInt64(n.start, low)
	if ownSize > 0 {
		own := runStats{}
		if n.count == 0 {
			own = runStats{prefix: ownSize, suffix: ownSize, max: ownSize}
		}
		stats = mergeRun(stats, own, size, ownSize)
		size += ownSize
	}
	rightStats, rightSize := t.rangeStats(n.right, low, high)
	if rightSize > 0 {
		stats = mergeRun(stats, rightStats, size, rightSize)
		size += rightSize
	}
	return stats, size
}

func (t *blockTree) rangeMinNode(n *blockNode, low, high int64) int {
	if n == nil || low >= high || high <= n.firstStart || low >= n.lastEnd {
		return 3
	}
	if low <= n.firstStart && high >= n.lastEnd {
		for i := 0; i < 3; i++ {
			if n.stats[i].max > 0 {
				return i
			}
		}
		return 3
	}
	t.push(n)
	result := 3
	if low < n.end && high > n.start {
		result = minInt(result, n.count)
	}
	result = minInt(result, t.rangeMinNode(n.left, low, high))
	result = minInt(result, t.rangeMinNode(n.right, low, high))
	return result
}

func (t *blockTree) rightmostFree(length int64) (int64, bool) {
	return t.rightmostFreeNode(t.root, length)
}

func (t *blockTree) rightmostFreeNode(n *blockNode, length int64) (int64, bool) {
	if n == nil || n.stats[0].max < length {
		return 0, false
	}
	t.visited++
	t.push(n)
	if pos, ok := t.rightmostFreeNode(n.right, length); ok {
		t.pull(n)
		return pos, true
	}
	if freeAcrossNodeRight(n, length) {
		pos := n.right.firstStart + n.right.stats[0].prefix - length
		t.pull(n)
		return pos, true
	}
	if n.count == 0 && n.end-n.start >= length {
		pos := n.end - length
		t.pull(n)
		return pos, true
	}
	if freeAcrossNodeLeft(n, length) {
		pos := n.end - n.left.stats[0].suffix - length
		t.pull(n)
		return pos, true
	}
	if pos, ok := t.rightmostFreeNode(n.left, length); ok {
		t.pull(n)
		return pos, true
	}
	t.pull(n)
	return 0, false
}

func freeAcrossNodeRight(n *blockNode, length int64) bool {
	if n.right == nil || n.count != 0 {
		return false
	}
	return n.end-n.start+n.right.stats[0].prefix >= length
}

func freeAcrossNodeLeft(n *blockNode, length int64) bool {
	if n.left == nil || n.count != 0 {
		return false
	}
	return n.left.stats[0].suffix+n.end-n.start >= length
}

func (t *blockTree) pull(n *blockNode) {
	if n == nil {
		return
	}
	n.size = n.end - n.start
	n.firstStart = n.start
	n.lastEnd = n.end
	for i := range n.stats {
		n.stats[i] = runStats{}
	}
	count := n.count
	if count > 2 {
		count = 2
	}
	n.stats[count] = runStats{prefix: n.size, suffix: n.size, max: n.size}
	if n.left != nil {
		n.firstStart = n.left.firstStart
		n.size += n.left.size
		for i := 0; i < 3; i++ {
			n.stats[i] = mergeRun(n.left.stats[i], n.stats[i], n.left.size, n.end-n.start)
		}
	}
	if n.right != nil {
		n.lastEnd = n.right.lastEnd
		n.size += n.right.size
		for i := 0; i < 3; i++ {
			leftSize := n.end - n.start
			if n.left != nil {
				leftSize += n.left.size
			}
			n.stats[i] = mergeRun(n.stats[i], n.right.stats[i], leftSize, n.right.size)
		}
	}
}

func mergeRun(a, b runStats, aSize, bSize int64) runStats {
	result := runStats{prefix: a.prefix, suffix: b.suffix, max: maxInt64(a.max, b.max)}
	result.max = maxInt64(result.max, a.suffix+b.prefix)
	if a.prefix == aSize && a.prefix > 0 {
		result.prefix = a.prefix + b.prefix
	}
	if b.suffix == bSize && b.suffix > 0 {
		result.suffix = b.suffix + a.suffix
	}
	return result
}

func (t *blockTree) apply(n *blockNode, delta int) {
	if n == nil {
		return
	}
	oldStats := n.stats
	newStats := [3]runStats{}
	for i := 0; i < 3; i++ {
		j := i + delta
		if j >= 0 && j < 3 {
			newStats[j] = oldStats[i]
		}
	}
	n.stats = newStats
	n.count += delta
	n.lazy += delta
}

func (t *blockTree) push(n *blockNode) {
	if n == nil || n.lazy == 0 {
		return
	}
	t.apply(n.left, n.lazy)
	t.apply(n.right, n.lazy)
	n.lazy = 0
}

func (t *blockTree) split(n *blockNode, key int64) (*blockNode, *blockNode) {
	if n == nil {
		return nil, nil
	}
	t.push(n)
	if key <= n.start {
		left, right := t.split(n.left, key)
		n.left = right
		t.pull(n)
		return left, n
	}
	if key >= n.end {
		left, right := t.split(n.right, key)
		n.right = left
		t.pull(n)
		return n, right
	}
	leftPart := t.newRun(n.start, key, n.count)
	rightPart := t.newRun(key, n.end, n.count)
	leftPart.priority = n.priority
	rightPart.priority = n.priority
	leftPart.left = n.left
	rightPart.right = n.right
	t.pull(leftPart)
	t.pull(rightPart)
	return leftPart, rightPart
}

func (t *blockTree) merge(a, b *blockNode) *blockNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		t.push(a)
		a.right = t.merge(a.right, b)
		t.pull(a)
		return a
	}
	t.push(b)
	b.left = t.merge(a, b.left)
	t.pull(b)
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
