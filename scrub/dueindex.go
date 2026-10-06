package scrub

// dueIndex 按 (到期时刻, 块号) 维护全部块的到期顺序。
//
// 底层是随机优先级 treap（键序即中序序）：
//   - upsert/remove：期望 O(log n) 次键比较；
//   - enumerate：对到期块做中序枚举，每访问一个键仅做 1 次与 t 的
//     比较，沿 left/right 指针推进不做键比较，因此比较次数恰为
//     “被访问条目数”（含越过 t 的那一条），与块总数无关。
//
// 比较次数经 comparisons() 暴露，供复杂度测试直接核验。
type treapNode struct {
	key         dueEntry
	prio        uint32
	left, right *treapNode
}

type dueIndex struct {
	root     *treapNode
	pos      map[int]dueEntry
	rngState uint32
	cmps     int64
}

func newDueIndex() *dueIndex {
	return &dueIndex{pos: make(map[int]dueEntry), rngState: 0x9e3779b9}
}

func (d *dueIndex) randPrio() uint32 {
	// xorshift32：确定性伪随机优先级。
	x := d.rngState
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	d.rngState = x
	return x
}

// less 是 treap 唯一的键比较入口：a < b（先比到期时刻，再比块号）。
func (d *dueIndex) less(a, b dueEntry) bool {
	d.cmps++
	if a.dueAt != b.dueAt {
		return a.dueAt < b.dueAt
	}
	return a.block < b.block
}

func (d *dueIndex) insert(n *treapNode, k dueEntry) *treapNode {
	if n == nil {
		return &treapNode{key: k, prio: d.randPrio()}
	}
	if d.less(k, n.key) {
		n.left = d.insert(n.left, k)
		if n.left.prio > n.prio {
			n = d.rotateRight(n)
		}
	} else {
		n.right = d.insert(n.right, k)
		if n.right.prio > n.prio {
			n = d.rotateLeft(n)
		}
	}
	return n
}

func (d *dueIndex) rotateRight(n *treapNode) *treapNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func (d *dueIndex) rotateLeft(n *treapNode) *treapNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func (d *dueIndex) erase(n *treapNode, k dueEntry) *treapNode {
	if n == nil {
		return nil
	}
	if d.less(k, n.key) {
		n.left = d.erase(n.left, k)
	} else if d.less(n.key, k) {
		n.right = d.erase(n.right, k)
	} else {
		switch {
		case n.left == nil:
			return n.right
		case n.right == nil:
			return n.left
		case n.left.prio > n.right.prio:
			n = d.rotateRight(n)
			n.right = d.erase(n.right, k)
		default:
			n = d.rotateLeft(n)
			n.left = d.erase(n.left, k)
		}
	}
	return n
}

func (d *dueIndex) upsert(entry dueEntry) {
	if old, ok := d.pos[entry.block]; ok {
		d.root = d.erase(d.root, old)
	}
	d.root = d.insert(d.root, entry)
	d.pos[entry.block] = entry
}

func (d *dueIndex) remove(block int) bool {
	old, ok := d.pos[block]
	if !ok {
		return false
	}
	d.root = d.erase(d.root, old)
	delete(d.pos, block)
	return true
}

// enumerate 按键序枚举 dueAt <= t 的条目；fn 返回 false 立即停止。
// 对每个被访问的键恰好做一次与 t 的比较（计入 comparisons），
// 沿指针下降不产生键比较，故比较总数等于被访问键数。
func (d *dueIndex) enumerate(t Time, fn func(dueEntry) bool) {
	var stack []*treapNode
	cur := d.root
	for cur != nil {
		stack = append(stack, cur)
		cur = cur.left
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		d.cmps++
		if n.key.dueAt > t {
			return // 键序保证后续条目到期时刻更大
		}
		if !fn(n.key) {
			return
		}

		cur = n.right
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
	}
}

func (d *dueIndex) comparisons() int64 { return d.cmps }
