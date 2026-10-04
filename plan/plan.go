// Package plan 维护每台设备的缺口序号运行集，并负责超时结算与补传请求切片。
package plan

import "container/heap"

// Range 是闭区间序号请求 [A,B]。
type Range struct {
	A int64
	B int64
}

// Sink 是缺口集对设备归宿表的回调。
type Sink interface {
	// LostRange 报告半开区间 [l,r) 内的序号被判丢（超时 R 次或 lo 抬升）。
	LostRange(l, r int64)
}

// Config 是规划器构造参数。
type Config struct {
	Lm int64 // 单个请求最大长度
	K  int   // 每次规划请求数上限
	Tq int64 // 请求超时（毫秒）
	R  int   // 每个序号请求次数上限
}

// GapSet 是一台设备的全部 Missing 序号运行集。
// 每个运行段带请求次数 c；在途段额外带本次请求时刻 q。
type GapSet struct {
	cfg         Config
	root        *rnode
	times       minHeap // 在途段按 q 排序的最小堆
	miss        int64   // 全部 Missing 序号数（含在途）
	emitScanned int     // 最近一次 Emit 考察的可请求段数
}

// rnode 是缺口运行段 AVL 树节点，代表半开区间 [l,r)。
type rnode struct {
	l, r     int64
	c        int // 已请求次数
	q        int64
	inflight bool
	dead     bool // 已从树中摘除，堆项据此判陈旧
	left     *rnode
	right    *rnode
	height   int8
	sum      int64
	segs     int
	reqSub   bool // 子树（含自身）是否存在可请求段
	heapIdx  int
}

func rh(n *rnode) int8 {
	if n == nil {
		return 0
	}
	return n.height
}

func (n *rnode) pull() {
	n.height = 1 + rh(n.left)
	if rh(n.right) > n.height-1 {
		n.height = 1 + rh(n.right)
	}
	n.sum = n.r - n.l
	n.segs = 1
	n.reqSub = !n.inflight
	if n.left != nil {
		n.sum += n.left.sum
		n.segs += n.left.segs
		n.reqSub = n.reqSub || n.left.reqSub
	}
	if n.right != nil {
		n.sum += n.right.sum
		n.segs += n.right.segs
		n.reqSub = n.reqSub || n.right.reqSub
	}
}

func rRotateRight(n *rnode) *rnode {
	x := n.left
	n.left = x.right
	x.right = n
	n.pull()
	x.pull()
	return x
}

func rRotateLeft(n *rnode) *rnode {
	x := n.right
	n.right = x.left
	x.left = n
	n.pull()
	x.pull()
	return x
}

func rebalance(n *rnode) *rnode {
	n.pull()
	if rh(n.left)-rh(n.right) > 1 {
		if rh(n.left.right) > rh(n.left.left) {
			n.left = rRotateLeft(n.left)
		}
		return rRotateRight(n)
	}
	if rh(n.right)-rh(n.left) > 1 {
		if rh(n.right.left) > rh(n.right.right) {
			n.right = rRotateRight(n.right)
		}
		return rRotateLeft(n)
	}
	return n
}

// eraseR 从以 root 为根的树中删除节点 z（按其左端点 l 定位），
// z 标记 dead：在途节点不立即从堆摘除，留在堆中作陈旧项，结算到堆顶时丢弃。
func (g *GapSet) eraseR(root, z *rnode) *rnode {
	if root == nil {
		return nil
	}
	switch {
	case z.l < root.l:
		root.left = g.eraseR(root.left, z)
	case z.l > root.l:
		root.right = g.eraseR(root.right, z)
	default:
		return eraseRNode(root)
	}
	return rebalance(root)
}

// eraseRNode 删除定位到的节点本身并合并其左右子树。
func eraseRNode(n *rnode) *rnode {
	n.dead = true
	if n.left == nil {
		return n.right
	}
	if n.right == nil {
		return n.left
	}
	var x *rnode
	if rh(n.left) > rh(n.right) {
		n.left, x = removeRMax(n.left)
	} else {
		n.right, x = removeRMin2(n.right)
	}
	x.left, x.right = n.left, n.right
	x.pull()
	return rebalance(x)
}

func removeRMax(n *rnode) (*rnode, *rnode) {
	if n.right == nil {
		x := n.left
		n.left = nil
		n.pull()
		return x, n
	}
	var x *rnode
	n.right, x = removeRMax(n.right)
	return rebalance(n), x
}

func removeRMin2(n *rnode) (*rnode, *rnode) {
	if n.left == nil {
		x := n.right
		n.right = nil
		n.pull()
		return x, n
	}
	var x *rnode
	n.left, x = removeRMin2(n.left)
	return rebalance(n), x
}

// putR 把不与任何既有段相交的 z 插入树中（不做相邻合并）。
func putR(n, z *rnode) *rnode {
	if n == nil {
		return z
	}
	if z.r <= n.l {
		n.left = putR(n.left, z)
	} else {
		n.right = putR(n.right, z)
	}
	return rebalance(n)
}

// findAt 返回覆盖序号 x 的段。
func findAt(n *rnode, x int64) *rnode {
	for n != nil {
		switch {
		case x < n.l:
			n = n.left
		case x >= n.r:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// compatible 报告两段是否可规范化为同一请求：相邻、均不在途且 c 相同。
func compatible(a, b *rnode) bool {
	return !a.inflight && !b.inflight && a.c == b.c
}

// minHeap 是在途段按 q 排序的最小堆。
type minHeap []*rnode

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].q != h[j].q {
		return h[i].q < h[j].q
	}
	return h[i].l < h[j].l
}
func (h minHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx, h[j].heapIdx = i, j
}
func (h *minHeap) Push(x any) {
	z := x.(*rnode)
	z.heapIdx = len(*h)
	*h = append(*h, z)
}
func (h *minHeap) Pop() any {
	old := *h
	z := old[len(old)-1]
	*h = old[:len(old)-1]
	return z
}

// New 创建缺口集。
func New(cfg Config) *GapSet { return &GapSet{cfg: cfg} }

// AddRun 声明半开区间 [l,r) 为新的 c=0 缺口（hi 推进时使用），
// 与相邻的 c=0 可请求段自动合并。
func (g *GapSet) AddRun(l, r int64) {
	if r <= l {
		return
	}
	g.miss += r - l
	g.root = g.addRun(g.root, l, r)
}

func (g *GapSet) addRun(n *rnode, l, r int64) *rnode {
	if n == nil {
		return g.newNode(l, r, 0, false, 0)
	}
	// 与可请求 c=0 段半开相邻或相交：并入后继续向外侧吸收。
	if !n.inflight && n.c == 0 && !(r < n.l || l > n.r) {
		return g.mergePut(n, l, r, 0)
	}
	if r <= n.l {
		n.left = g.addRun(n.left, l, r)
	} else {
		n.right = g.addRun(n.right, l, r)
	}
	return rebalance(n)
}

func (g *GapSet) newNode(l, r int64, c int, inflight bool, q int64) *rnode {
	z := &rnode{l: l, r: r, c: c, inflight: inflight, q: q, heapIdx: -1}
	z.pull()
	return z
}

// mergePut 在当前递归根 n 恰为同 c 可请求且接触段时，并入 [l,r)，
// 删除 n 后继续向两侧吸收，返回新子树根。
func (g *GapSet) mergePut(n *rnode, l, r int64, c int) *rnode {
	if l > n.l {
		l = n.l
	}
	if r < n.r {
		r = n.r
	}
	root := eraseRNode(n)
	root = g.refill(root, l, r, c)
	return root
}

// Fill 报告序号 x 已到达：把它移出缺口集（含在途段的剩余序号拆分）。
func (g *GapSet) Fill(x int64) {
	n := findAt(g.root, x)
	if n == nil {
		return
	}
	g.miss--
	g.root = g.eraseR(g.root, n)
	if n.inflight {
		if n.l < x {
			z := g.newNode(n.l, x, n.c, true, n.q)
			g.root = putR(g.root, z)
			heap.Push(&g.times, z)
		}
		if x+1 < n.r {
			z := g.newNode(x+1, n.r, n.c, true, n.q)
			g.root = putR(g.root, z)
			heap.Push(&g.times, z)
		}
		return
	}
	g.root = g.refill(g.root, n.l, x, n.c)
	g.root = g.refill(g.root, x+1, n.r, n.c)
}

// refill 把半开 [l,r) 作为 c 次可请求段重新并入，与相邻同 c 可请求段合并。
func (g *GapSet) refill(n *rnode, l, r int64, c int) *rnode {
	if r <= l {
		return n
	}
	if n != nil && !n.inflight && n.c == c && !(r < n.l || l > n.r) {
		return g.mergePut(n, l, r, c)
	}
	if n == nil {
		return g.newNode(l, r, c, false, 0)
	}
	if r <= n.l {
		n.left = g.refill(n.left, l, r, c)
	} else {
		n.right = g.refill(n.right, l, r, c)
	}
	return rebalance(n)
}

// LoseBelow 把与半开区间 [lo,r) 相交的缺口整体挖出并逐段回调 LostRange；
// 即使序号在途也立即判丢（lo 抬升时使用）。返回挖出的段数。
func (g *GapSet) LoseBelow(sink Sink, lo, r int64) int {
	cut := 0
	g.root, cut, g.miss = g.cut(g.root, lo, r, sink, 0, g.miss)
	return cut
}

// cut 从子树挖出与 [lo,r) 相交部分，回调 sink，返回新根、挖出段数与剩余 miss。
func (g *GapSet) cut(n *rnode, lo, r int64, sink Sink, pieces int, miss int64) (*rnode, int, int64) {
	if n == nil {
		return nil, pieces, miss
	}
	if r <= n.l {
		var c int
		n.left, c, miss = g.cut(n.left, lo, r, sink, pieces, miss)
		return rebalance(n), c, miss
	}
	if lo >= n.r {
		var c int
		n.right, c, miss = g.cut(n.right, lo, r, sink, pieces, miss)
		return rebalance(n), c, miss
	}
	n.left, pieces, miss = g.cut(n.left, lo, r, sink, pieces, miss)
	n.right, pieces, miss = g.cut(n.right, lo, r, sink, pieces, miss)
	ll := max64(n.l, lo)
	rr := min64(n.r, r)
	if ll < rr {
		sink.LostRange(ll, rr)
		pieces++
		miss -= rr - ll
	}
	root := eraseRNode(n)
	if n.inflight {
		if n.l < ll {
			z := g.newNode(n.l, ll, n.c, true, n.q)
			root = putR(root, z)
			heap.Push(&g.times, z)
		}
		if rr < n.r {
			z := g.newNode(rr, n.r, n.c, true, n.q)
			root = putR(root, z)
			heap.Push(&g.times, z)
		}
	} else {
		root = g.refill(root, n.l, ll, n.c)
		root = g.refill(root, rr, n.r, n.c)
	}
	return root, pieces, miss
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Settle 结算 q 满足 now-q>=Tq 的在途段：c+1>=R 者回调 LostRange 判丢，
// 其余翻回可请求并保留次数；再规范化合并相邻同 c 可请求段。返回结算段数。
func (g *GapSet) Settle(sink Sink, now int64) int {
	settled := 0
	for g.times.Len() > 0 {
		if g.times[0].q+g.cfg.Tq > now {
			break
		}
		n := heap.Pop(&g.times).(*rnode)
		if n.dead {
			continue
		}
		settled++
		g.root = g.eraseR(g.root, n)
		// 本次请求已是第 n.c 次请求：第 R 次请求仍超时则判丢；
		// 否则该序号可再被请求（次数已在发出时计入），翻回时保留 n.c。
		if n.c >= g.cfg.R {
			sink.LostRange(n.l, n.r)
			g.miss -= n.r - n.l
			continue
		}
		g.root = g.refill(g.root, n.l, n.r, n.c)
	}
	return settled
}

// firstReqAt 返回子树中端点 >= at 的最早可请求段；不存在返回 nil。
func firstReqAt(n *rnode, at int64) *rnode {
	if n == nil {
		return nil
	}
	if n.r <= at {
		return firstReqAt(n.right, at)
	}
	if n.left != nil && n.left.reqSub {
		if z := firstReqAt(n.left, at); z != nil {
			return z
		}
	}
	if !n.inflight && n.r > at {
		return n
	}
	return firstReqAt(n.right, at)
}

// Emit 生成至多 K 个、序号总数不超过 budget 的请求并推进在途状态。
func (g *GapSet) Emit(now int64, budget int64) []Range {
	var out []Range
	var at int64
	g.emitScanned = 0
	for budget > 0 && len(out) < g.cfg.K {
		n := firstReqAt(g.root, at)
		if n == nil {
			break
		}
		g.emitScanned++
		cnt := min64(n.r-n.l, g.cfg.Lm)
		cnt = min64(cnt, budget)
		a := n.l
		b := a + cnt - 1
		out = append(out, Range{A: a, B: b})
		budget -= cnt
		g.root = g.eraseR(g.root, n)
		in := g.newNode(a, b+1, n.c+1, true, now)
		g.root = putR(g.root, in)
		heap.Push(&g.times, in)
		if n.l < a {
			g.root = g.refill(g.root, n.l, a, n.c)
		}
		if b+1 < n.r {
			g.root = g.refill(g.root, b+1, n.r, n.c)
		}
		at = b + 1
	}
	return out
}

// Missing 返回仍无归宿（含在途）的序号总数。
func (g *GapSet) Missing() int64 { return g.miss }

// Inflight 报告 x 当前是否在途。
func (g *GapSet) Inflight(x int64) bool {
	n := findAt(g.root, x)
	return n != nil && n.inflight
}

// Requested 返回 x 已被请求次数 c（非缺口返回 0）。
func (g *GapSet) Requested(x int64) int {
	n := findAt(g.root, x)
	if n == nil {
		return 0
	}
	return n.c
}

// Segments 返回当前运行段数（测试/观测用）。
func (g *GapSet) Segments() int {
	if g.root == nil {
		return 0
	}
	return g.root.segs
}

// EmitScanned 返回最近一次 Emit 考察的可请求段数（visited 证明用）。
func (g *GapSet) EmitScanned() int { return g.emitScanned }
