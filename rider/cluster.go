package rider

import "math/rand"

// 根因簇：同一骑手、同一 rootKey、事件按 (发生时刻, 登记序号) 排序后，
// 相邻发生时刻之差 <= ClusterSpan 的存活事件构成一个连通簇（传递延伸）。
//
// 数据结构：
//   - 桶 treap：按 rootKey 分桶，键 (time,seq) 有序；插入/近邻定位期望 O(log k)；
//   - dsu：节点 -> 簇代表；代表持有簇有序双向链表的 head/tail 与成员数；
//   - 新事件只与“左簇尾部”“右簇头部”比较间隙：只有跨过边界间隙才合并，
//     间隙大于跨度的两个已有簇永远不会被新事件误并。
// 一次登记只触达至多两个相邻簇，不扫描骑手历史。

type cKey struct {
	t   int64
	seq int64
}

func (k cKey) less(o cKey) bool { return k.t < o.t || (k.t == o.t && k.seq < o.seq) }

type cNode struct {
	key      cKey
	ev       *Event
	priority uint64
	left     *cNode // 桶 treap
	right    *cNode
	tparent  *cNode // 桶 treap 父指针（中序前后继回溯用）

	dprev *cNode // 簇内有序双向链表
	dnext *cNode
}

type cRep struct {
	head *cNode
	tail *cNode
	size int
}

type clusterIndex struct {
	trees map[string]*cNode
	dsu   map[*cNode]*cRep
	rng   *rand.Rand
}

func newClusterIndex() *clusterIndex {
	return &clusterIndex{
		trees: map[string]*cNode{},
		dsu:   map[*cNode]*cRep{},
		rng:   rand.New(rand.NewSource(0x51d3c0de)),
	}
}

func (ci *clusterIndex) insert(s *System, rs *riderState, e *Event) {
	myScore := s.cfg.EventScores[e.Type]
	if e.RootKey == "" {
		ci.addCounting(s, rs, e, myScore)
		return
	}

	k := cKey{t: e.Time, seq: e.Seq}
	nn := &cNode{key: k, ev: e, priority: ci.rng.Uint64()}

	root, rawPred, rawSucc := treapInsert(ci.trees[e.RootKey], nn)
	ci.trees[e.RootKey] = root

	// 定位物理边界：左簇尾部与右簇头部。
	var leftTail, rightHead *cNode
	if rawPred != nil {
		leftTail = ci.dsu[rawPred].tail
	}
	if rawSucc != nil {
		rightHead = ci.dsu[rawSucc].head
	}

	nnRep := &cRep{head: nn, tail: nn, size: 1}
	ci.dsu[nn] = nnRep

	mergeLeft := leftTail != nil && e.Time-leftTail.key.t <= s.cfg.ClusterSpan
	mergeRight := rightHead != nil && rightHead.key.t-e.Time <= s.cfg.ClusterSpan

	// 记录合并前所有相邻簇的最早者，合并后统一对账计分。
	oldMin := nn
	var reps []*cRep // 合并前参与方代表（nn 与其相邻簇），用于对账旧最早者
	reps = append(reps, nnRep)
	if mergeLeft {
		if h := ci.dsu[leftTail].head; h.key.less(oldMin.key) {
			oldMin = h
		}
		reps = append(reps, ci.dsu[leftTail])
	}
	if mergeRight {
		if h := ci.dsu[rightHead].head; h.key.less(oldMin.key) {
			oldMin = h
		}
		reps = append(reps, ci.dsu[rightHead])
	}

	if mergeLeft {
		nnRep = ci.union(nnRep, ci.dsu[leftTail])
	}
	if mergeRight {
		nnRep = ci.union(nnRep, ci.dsu[rightHead])
	}

	// 合并簇唯一计扣分者为全局最早者。
	newMin := ci.dsu[nn].head
	seen := map[int64]bool{}
	for _, rep := range reps {
		h := rep.head
		if h.key != newMin.key && !seen[h.ev.ID] {
			ci.removeCounting(s, rs, h.ev)
			seen[h.ev.ID] = true
		}
	}
	ci.addCounting(s, rs, newMin.ev, s.cfg.EventScores[newMin.ev.Type])
}

// union 合并两个簇代表，返回合并后的代表（锚点为 a 所在簇，用于调用方继续定位）。
func (ci *clusterIndex) union(a, b *cRep) *cRep {
	if a == b {
		return a
	}

	// 拼接前收集 b 的全部成员，稍后统一改指。
	var bMembers []*cNode
	for x, n := b.head, b.size; x != nil && n > 0; x, n = x.dnext, n-1 {
		bMembers = append(bMembers, x)
	}

	// 键序拼接（两个簇在物理上相邻）。
	if a.tail.key.less(b.head.key) {
		a.tail.dnext = b.head
		b.head.dprev = a.tail
		a.tail = b.tail
	} else {
		b.tail.dnext = a.head
		a.head.dprev = b.tail
		a.head = b.head
	}
	a.size += b.size
	for _, x := range bMembers {
		ci.dsu[x] = a
	}
	return a
}

func (ci *clusterIndex) addCounting(sys *System, rs *riderState, e *Event, score int) {
	ps := rs.period(sys.periodOf(e.Time))
	if _, ok := ps.counting[e.ID]; ok {
		return
	}
	ps.counting[e.ID] = score
	ps.liveScore += score
}

func (ci *clusterIndex) removeCounting(sys *System, rs *riderState, e *Event) {
	ps := rs.period(sys.periodOf(e.Time))
	score, ok := ps.counting[e.ID]
	if !ok {
		return
	}
	delete(ps.counting, e.ID)
	ps.liveScore -= score
}

// revokeCluster 撤销 e 所在簇的全部存活事件；无根因事件自成一簇。
func (ci *clusterIndex) revokeCluster(s *System, _ *riderState, e *Event) []int64 {
	if e.RootKey == "" {
		return []int64{e.ID}
	}
	node := treapFind(ci.trees[e.RootKey], cKey{t: e.Time, seq: e.Seq})
	if node == nil {
		return []int64{e.ID}
	}
	rep := ci.dsu[node]
	var ids []int64
	count := rep.size
	var erased []*cNode
	for x := rep.head; x != nil && count > 0; x = x.dnext {
		ids = append(ids, x.ev.ID)
		erased = append(erased, x)
		count--
	}
	tree := ci.trees[e.RootKey]
	for _, id := range ids {
		ev := s.events[id]
		k := cKey{t: ev.Time, seq: ev.Seq}
		tree = treapErase(tree, k)
	}
	ci.trees[e.RootKey] = tree
	for _, x := range erased {
		delete(ci.dsu, x)
	}
	return ids
}

// --- 桶 treap 原语 ---

func rotateRight(n *cNode) *cNode {
	l := n.left
	n.left = l.right
	if l.right != nil {
		l.right.tparent = n
	}
	l.right = n
	l.tparent = n.tparent
	n.tparent = l
	return l
}

func rotateLeft(n *cNode) *cNode {
	r := n.right
	n.right = r.left
	if r.left != nil {
		r.left.tparent = n
	}
	r.left = n
	r.tparent = n.tparent
	n.tparent = r
	return r
}

func treapInsert(root, nn *cNode) (*cNode, *cNode, *cNode) {
	pred, succ := nearest(root, nn.key)
	parent := pred
	if succ != nil && (pred == nil || !pred.key.less(succ.key)) {
		parent = succ
	}
	nn.tparent = parent
	if parent == nil {
		return nn, nil, nil
	}
	if nn.key.less(parent.key) {
		parent.left = nn
	} else {
		parent.right = nn
	}
	x := nn
	for x.tparent != nil && x.priority > x.tparent.priority {
		p := x.tparent
		gp := p.tparent
		var newSub *cNode
		if x == p.left {
			newSub = rotateRight(p)
		} else {
			newSub = rotateLeft(p)
		}
		newSub.tparent = gp
		if gp != nil {
			if gp.left == p {
				gp.left = newSub
			} else {
				gp.right = newSub
			}
		}
	}
	for root.tparent != nil {
		root = root.tparent
	}
	return root, pred, succ
}

func nearest(root *cNode, k cKey) (pred, succ *cNode) {
	for root != nil {
		if k.less(root.key) {
			succ = root
			root = root.left
		} else {
			pred = root
			root = root.right
		}
	}
	return pred, succ
}

func inorderPred(root *cNode) *cNode {
	if root.left != nil {
		x := root.left
		for x.right != nil {
			x = x.right
		}
		return x
	}
	x := root
	p := x.tparent
	for p != nil && x == p.left {
		x, p = p, p.tparent
	}
	return p
}

func inorderSucc(root *cNode) *cNode {
	if root.right != nil {
		x := root.right
		for x.left != nil {
			x = x.left
		}
		return x
	}
	x := root
	p := x.tparent
	for p != nil && x == p.right {
		x, p = p, p.tparent
	}
	return p
}

func treapErase(root *cNode, k cKey) *cNode {
	if root == nil {
		return nil
	}
	if k.less(root.key) {
		root.left = treapErase(root.left, k)
		if root.left != nil {
			root.left.tparent = root
		}
	} else if root.key.less(k) {
		root.right = treapErase(root.right, k)
		if root.right != nil {
			root.right.tparent = root
		}
	} else {
		if root.left == nil {
			if root.right != nil {
				root.right.tparent = root.tparent
			}
			return root.right
		}
		if root.right == nil {
			root.left.tparent = root.tparent
			return root.left
		}
		if root.left.priority > root.right.priority {
			gp := root.tparent
			root = rotateRight(root)
			root.tparent = gp
			root.right = treapErase(root.right, k)
			if root.right != nil {
				root.right.tparent = root
			}
		} else {
			gp := root.tparent
			root = rotateLeft(root)
			root.tparent = gp
			root.left = treapErase(root.left, k)
			if root.left != nil {
				root.left.tparent = root
			}
		}
	}
	return root
}

func treapFind(root *cNode, k cKey) *cNode {
	for root != nil {
		switch {
		case k.less(root.key):
			root = root.left
		case root.key.less(k):
			root = root.right
		default:
			return root
		}
	}
	return nil
}
