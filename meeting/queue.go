package meeting

// handQueue 是容量受限的 FIFO 举手队列。
//
// 为满足“移出任意成员、查询名次不得随队列长度线性增长”的要求，
// 底层使用以进入序号 seq 为键、随机优先级为堆序的 Treap：
//   - 入队：seq 单调递增，新键恒为最大键，merge 到最右链；
//   - 出队/移出：按 seq 分裂再合并；
//   - 名次查询：利用子树 size 做秩查询；
//
// 以上操作期望复杂度均为 O(log n)，n 为队列长度。
//
// visits 统计访问过的结点数，供测试以可验证方式证明复杂度上界。
type handQueue struct {
	root    *qnode
	byUser  map[string]uint64 // user -> 进入序号
	nextSeq uint64
	rng     splitmix64
	visits  int // 自上次 resetVisits 以来访问的结点数（测试探针）
}

type qnode struct {
	user        string
	seq         uint64
	prio        uint64
	left, right *qnode
	size        int
}

// splitmix64 是确定性的伪随机源，种子只取决于会议室配置，
// 保证相同操作序列重放时 Treap 形态也完全一致。
type splitmix64 struct{ s uint64 }

func (r *splitmix64) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func newHandQueue(seed uint64) *handQueue {
	return &handQueue{
		byUser:  make(map[string]uint64),
		nextSeq: 1, // 从 1 开始，避免按 seq-1 分裂时下溢
		rng:     splitmix64{s: seed},
	}
}

func nodeSize(n *qnode) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *qnode) pull() {
	n.size = 1 + nodeSize(n.left) + nodeSize(n.right)
}

// split 把 t 分裂为 l（键 <= key）与 r（键 > key）。
func (q *handQueue) split(t *qnode, key uint64) (l, r *qnode) {
	if t == nil {
		return nil, nil
	}
	q.visits++
	if t.seq <= key {
		l = t
		var sub *qnode
		sub, r = q.split(t.right, key)
		l.right = sub
		l.pull()
		return l, r
	}
	r = t
	var sub *qnode
	l, sub = q.split(t.left, key)
	r.left = sub
	r.pull()
	return l, r
}

// merge 合并两棵树，要求 a 中所有键小于 b 中所有键。
func (q *handQueue) merge(a, b *qnode) *qnode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	q.visits++
	if a.prio < b.prio {
		a.right = q.merge(a.right, b)
		a.pull()
		return a
	}
	b.left = q.merge(a, b.left)
	b.pull()
	return b
}

func (q *handQueue) len() int { return nodeSize(q.root) }

func (q *handQueue) contains(user string) bool {
	_, ok := q.byUser[user]
	return ok
}

// pushBack 把 user 追加到队尾。调用方保证 user 不在队列中且队列未满。
func (q *handQueue) pushBack(user string) {
	nd := &qnode{user: user, seq: q.nextSeq, prio: q.rng.next(), size: 1}
	q.byUser[user] = q.nextSeq
	q.nextSeq++
	q.root = q.merge(q.root, nd) // 新键恒为最大键
}

// remove 移出队列中的 user。调用方保证 user 在队列中。
func (q *handQueue) remove(user string) {
	seq := q.byUser[user]
	delete(q.byUser, user)
	le, ge := q.split(q.root, seq-1) // le: 键 < seq；ge: 键 >= seq
	_, gt := q.split(ge, seq)        // 丢弃恰含该结点的中段
	q.root = q.merge(le, gt)
}

// front 返回队首成员。
func (q *handQueue) front() (string, bool) {
	n := q.root
	if n == nil {
		return "", false
	}
	for n.left != nil {
		q.visits++
		n = n.left
	}
	q.visits++
	return n.user, true
}

// popFront 移出并返回队首成员。
func (q *handQueue) popFront() (string, bool) {
	user, ok := q.front()
	if !ok {
		return "", false
	}
	seq := q.byUser[user]
	delete(q.byUser, user)
	_, rest := q.split(q.root, seq) // seq 是最小键，左半恰含队首
	q.root = rest
	return user, true
}

// pos 返回 user 的队列名次（队首为 1），不在队列中返回 0。
// 利用子树 size 做秩查询，代价 O(log n)。
func (q *handQueue) pos(user string) int {
	seq, ok := q.byUser[user]
	if !ok {
		return 0
	}
	rank := 0
	n := q.root
	for n != nil {
		q.visits++
		if n.seq < seq {
			rank += nodeSize(n.left) + 1
			n = n.right
		} else {
			n = n.left
		}
	}
	return rank + 1
}

// order 按从队首到队尾的次序返回全部成员（中序遍历）。
func (q *handQueue) order() []string {
	out := make([]string, 0, q.len())
	var walk func(n *qnode)
	walk = func(n *qnode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.user)
		walk(n.right)
	}
	walk(q.root)
	return out
}

// height 返回树高，仅供测试验证平衡性。
func (q *handQueue) height() int {
	var h func(n *qnode) int
	h = func(n *qnode) int {
		if n == nil {
			return 0
		}
		l, r := h(n.left), h(n.right)
		if l > r {
			return l + 1
		}
		return r + 1
	}
	return h(q.root)
}

func (q *handQueue) resetVisits() { q.visits = 0 }
