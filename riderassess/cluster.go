package riderassess

import "sort"

// 本文件负责"事件与根因簇"：按 (骑手,根因标识) 维护未撤销事件的有序集合，
// 用 treap 保序、并查集维护"相邻时刻差不超过 ClusterSpan"的传递连通块（簇）。
// 簇内最早（时刻最小，平局按事件 ID 字典序）的事件计扣分，其余为连带事件。
//
// 复杂度论证（与 DESIGN.md 对应）：
//   - treap 定位前驱/后继为 O(log m)，m 为该 (骑手,根因) 下未撤销事件数；
//   - 一次插入只可能与其直接前驱、直接后继所在的两个簇相连，与历史事件总数无关；
//   - 并查集按大小合并且带路径压缩，近常数时间；
//   - 整簇撤销按簇大小线性收费，撤销后节点离开全部结构，不参与后续任何操作。

type eventKey struct {
	at int64
	id string
}

func keyLess(a, b eventKey) bool { return a.at < b.at || (a.at == b.at && a.id < b.id) }

// tNode 是 treap 节点；dsu 为并查集父指针。
type tNode struct {
	key     eventKey
	prio    uint64
	left    *tNode
	right   *tNode
	dsu     *tNode   // 并查集父指针
	size    int      // 仅并查集根有效：簇大小
	members []*tNode // 仅并查集根有效：簇成员
	scoring bool     // 本事件是否为所在簇的计扣分者（簇内最早者）
}

func (n *tNode) find() *tNode {
	root := n
	for root.dsu != root {
		root = root.dsu
	}
	for x := n; x.dsu != x; {
		nx := x.dsu
		x.dsu = root
		x = nx
	}
	return root
}

// minMember 返回并查集根成员列表中键最小的节点（簇内最早者）。
func minMember(root *tNode) *tNode {
	best := root.members[0]
	for _, m := range root.members[1:] {
		if keyLess(m.key, best.key) {
			best = m
		}
	}
	return best
}

// rootSet 是一个 (骑手,根因标识) 下全部未撤销事件的集合。
type rootSet struct {
	root  *tNode
	index map[eventKey]*tNode
	seq   uint64 // 确定性 treap 优先级
}

func newRootSet() *rootSet {
	return &rootSet{index: map[eventKey]*tNode{}, seq: 0x9E3779B97F4A7C15}
}

func (s *rootSet) nextPrio() uint64 {
	// xorshift64*，仅决定树形状、不影响语义；固定种子保证重放结构一致。
	x := s.seq
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	s.seq = x
	return x * 0x2545F4914F6CDD1D
}

// insertResult 描述一次登记对"谁计扣分"的影响。
type insertResult struct {
	newScorer      bool       // 新事件成为簇内最早者，开始计扣分
	stoppedScorers []eventKey // 因新最早者出现而不再计扣分的旧最早者
}

// insertEvent 登记一个事件并完成簇归并。
func (s *rootSet) insertEvent(ev *Event, span int64) insertResult {
	k := eventKey{ev.OccurAt, ev.ID}
	if _, ok := s.index[k]; ok {
		return insertResult{} // System 保证事件 ID 全局唯一
	}
	pred, succ := s.neighbors(k)

	n := &tNode{key: k, prio: s.nextPrio()}
	n.dsu = n
	n.size = 1
	n.members = []*tNode{n}
	n.scoring = true
	s.root = treapInsert(s.root, n)
	s.index[k] = n

	// 可连通关系只能经由有序集合中的直接前驱/直接后继产生；
	// 前驱与后继可能属于同一个已有簇，需按并查集根去重。
	var groups []*tNode
	link := func(m *tNode, gap int64) {
		if m == nil || gap > span {
			return
		}
		r := m.find()
		for _, g := range groups {
			if g.find() == r {
				return
			}
		}
		groups = append(groups, r)
	}
	if pred != nil {
		link(pred, k.at-pred.key.at)
	}
	if succ != nil {
		link(succ, succ.key.at-k.at)
	}

	priorEarliest := make([]eventKey, 0, len(groups))
	for _, g := range groups {
		priorEarliest = append(priorEarliest, minMember(g).key)
	}
	merged := n
	for _, g := range groups {
		merged = union(merged, g)
	}
	earliest := minMember(merged.find()).key

	res := insertResult{newScorer: earliest == k}
	for _, e := range priorEarliest {
		if e != earliest {
			res.stoppedScorers = append(res.stoppedScorers, e)
		}
	}
	for _, e := range res.stoppedScorers {
		s.index[e].scoring = false
	}
	n.scoring = res.newScorer
	return res
}

// union 合并两个并查集根，按大小挂接，维护簇大小与成员列表。
// 不缓存"最早者"：成员列表 append 后由 minMember 现取，避免过期缓存。
func union(a, b *tNode) *tNode {
	a = a.find()
	b = b.find()
	if a == b {
		return a
	}
	if a.size < b.size {
		a, b = b, a
	}
	b.dsu = a
	a.size += b.size
	a.members = append(a.members, b.members...)
	return a
}

// revokeCluster 删除 key 所在簇的全部事件，按簇内最早者优先返回。
// 若 key 不在集合中（例如整簇此前已被撤销），返回 nil。
func (s *rootSet) revokeCluster(key eventKey) []eventKey {
	n, ok := s.index[key]
	if !ok {
		return nil
	}
	r := n.find()
	members := r.members
	out := make([]eventKey, 0, len(members))
	for _, m := range members {
		out = append(out, m.key)
		delete(s.index, m.key)
		s.root = treapDelete(s.root, m.key)
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i], out[j]) })
	return out
}

// isSatellite 判断 key 是否为连带事件（存在且不是簇内最早者）。
func (s *rootSet) isSatellite(key eventKey) bool {
	n, ok := s.index[key]
	return ok && !n.scoring
}

func (s *rootSet) len() int { return len(s.index) }

// neighbors 返回严格小于 k 的最大键节点与严格大于 k 的最小键节点。
func (s *rootSet) neighbors(k eventKey) (pred, succ *tNode) {
	for x := s.root; x != nil; {
		switch {
		case keyLess(k, x.key):
			succ = x
			x = x.left
		case keyLess(x.key, k):
			pred = x
			x = x.right
		default:
			return x, x
		}
	}
	return pred, succ
}

func rotRight(n *tNode) *tNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotLeft(n *tNode) *tNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

func treapInsert(root, n *tNode) *tNode {
	if root == nil {
		return n
	}
	if keyLess(n.key, root.key) {
		root.left = treapInsert(root.left, n)
		if root.left.prio > root.prio {
			root = rotRight(root)
		}
	} else {
		root.right = treapInsert(root.right, n)
		if root.right.prio > root.prio {
			root = rotLeft(root)
		}
	}
	return root
}

func treapDelete(root *tNode, k eventKey) *tNode {
	if root == nil {
		return nil
	}
	switch {
	case keyLess(k, root.key):
		root.left = treapDelete(root.left, k)
	case keyLess(root.key, k):
		root.right = treapDelete(root.right, k)
	default:
		if root.left == nil {
			return root.right
		}
		if root.right == nil {
			return root.left
		}
		if root.left.prio > root.right.prio {
			root = rotRight(root)
			root.right = treapDelete(root.right, k)
		} else {
			root = rotLeft(root)
			root.left = treapDelete(root.left, k)
		}
	}
	return root
}
