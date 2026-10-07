package booking

// ivlKind 区分日历占用区间的来源。
type ivlKind uint8

const (
	kindBooking ivlKind = iota // 有效保留或已确认预订
	kindBlock                  // 房东封锁
)

// ivl 是半开区间 [start, end) 的日历占用。
type ivl struct {
	start int
	end   int
	kind  ivlKind
	id    string
}

// tnode 是 treap 节点。优先级由起始日哈希确定，
// 因此同一操作序列重放得到完全相同的树形与日历。
type tnode struct {
	v    ivl
	prio uint64
	l, r *tnode
}

// treap 是以起始日为键、以哈希优先级平衡的区间集合。
// 区间互不相交，起始日唯一。steps 统计节点访问次数，
// 用于验证判定开销只随树高（O(log n)）增长。
type treap struct {
	root  *tnode
	steps int64
}

// prioOf 用 splitmix64 为起始日生成确定性优先级。
func prioOf(start int) uint64 {
	z := uint64(start)*0x9E3779B97F4A7C15 + 0x6A09E667F3BCC909
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// lowerBound 返回起始日 >= start 的最小区间。
func (t *treap) lowerBound(start int) (ivl, bool) {
	var best *tnode
	for n := t.root; n != nil; {
		t.steps++
		if n.v.start >= start {
			best = n
			n = n.l
		} else {
			n = n.r
		}
	}
	if best == nil {
		return ivl{}, false
	}
	return best.v, true
}

// predecessor 返回起始日 < start 的最大区间。
func (t *treap) predecessor(start int) (ivl, bool) {
	var best *tnode
	for n := t.root; n != nil; {
		t.steps++
		if n.v.start < start {
			best = n
			n = n.r
		} else {
			n = n.l
		}
	}
	if best == nil {
		return ivl{}, false
	}
	return best.v, true
}

func (t *treap) insert(v ivl) { t.root = t.ins(t.root, v) }

func (t *treap) ins(n *tnode, v ivl) *tnode {
	t.steps++
	if n == nil {
		return &tnode{v: v, prio: prioOf(v.start)}
	}
	if v.start < n.v.start {
		n.l = t.ins(n.l, v)
		if n.l.prio < n.prio {
			n = rotRight(n)
		}
	} else {
		n.r = t.ins(n.r, v)
		if n.r.prio < n.prio {
			n = rotLeft(n)
		}
	}
	return n
}

func (t *treap) remove(start int) { t.root = t.rem(t.root, start) }

func (t *treap) rem(n *tnode, start int) *tnode {
	t.steps++
	if n == nil {
		return nil
	}
	switch {
	case start < n.v.start:
		n.l = t.rem(n.l, start)
	case start > n.v.start:
		n.r = t.rem(n.r, start)
	default:
		return t.merge(n.l, n.r)
	}
	return n
}

func (t *treap) merge(l, r *tnode) *tnode {
	t.steps++
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio < r.prio {
		l.r = t.merge(l.r, r)
		return l
	}
	r.l = t.merge(l, r.l)
	return r
}

func rotLeft(n *tnode) *tnode {
	r := n.r
	n.r = r.l
	r.l = n
	return r
}

func rotRight(n *tnode) *tnode {
	l := n.l
	n.l = l.r
	l.r = n
	return l
}

// inorder 按起始日升序返回全部区间。
func (t *treap) inorder() []ivl {
	var out []ivl
	var walk func(n *tnode)
	walk = func(n *tnode) {
		if n == nil {
			return
		}
		walk(n.l)
		out = append(out, n.v)
		walk(n.r)
	}
	walk(t.root)
	return out
}
