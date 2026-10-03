package roadnet

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
)

const inf int64 = math.MaxInt64

// Leg 是路线的一段：在 Depart 时刻进入边 EdgeID，于 Arrive 时刻到达终点。
type Leg struct {
	EdgeID int
	Depart int64
	Arrive int64
}

// Result 是 EarliestArrival 的查询结果。
type Result struct {
	// Arrival 是到达 g 的最早时刻，等于 d(g) 与路线末段的到达时刻。
	Arrival int64
	// Route 只由紧边组成：在所有 s→g 紧边路径中先取边数最少者，
	// 再取边编号序列字典序最小者；每段出发时刻取可行最小整数。
	Route []Leg
	// popped 是本次查询从优先队列中确定（弹出）的节点数，
	// 用于证明搜索在 g 确定后立即停止而非求全图最短路。
	popped int
}

// Popped 返回本次查询从优先队列中确定的节点数。
func (r Result) Popped() int { return r.popped }

// openSeg 是一条绝对时刻下的开放分段 [start, end)，通行耗时 cost。
type openSeg struct {
	start, end, cost int64
}

// segList 是某条边在某个查询版本下可见记录展开后的开放分段序列，
// 带后缀最小值索引，用于对数时间求到达函数与最优出发时刻。
type segList struct {
	segs   []openSeg
	sufVal []int64 // sufVal[i] = min{ segs[j].start+segs[j].cost : j >= i }
	sufDep []int64 // sufDep[i] = 取得 sufVal[i] 的最小 segs[j].start
}

// buildSegList 把边 e 在版本 ver 下可见的记录展开为绝对时刻开放分段。
// 记录第 i 段自 eff+offset 起、到下一段起点或下一条可见记录的 eff 前有效
// （记录边界处的分段被截断）；封闭分段不进入列表。
func buildSegList(e *Edge, ver int64) *segList {
	vis := make([]*Record, 0, len(e.Records))
	for i := range e.Records {
		r := &e.Records[i]
		if r.RegVersion <= ver && (r.ReplVersion == 0 || r.ReplVersion > ver) {
			vis = append(vis, r)
		}
	}
	var segs []openSeg
	for k, r := range vis {
		recEnd := inf
		if k+1 < len(vis) {
			recEnd = vis[k+1].Eff
		}
		for i, seg := range r.Profile {
			if seg.Cost == ClosedCost {
				continue
			}
			start := r.Eff + seg.Offset
			end := recEnd
			if i+1 < len(r.Profile) {
				if nxt := r.Eff + r.Profile[i+1].Offset; nxt < end {
					end = nxt
				}
			}
			if start < end {
				segs = append(segs, openSeg{start: start, end: end, cost: seg.Cost})
			}
		}
	}
	m := len(segs)
	sufVal := make([]int64, m+1)
	sufDep := make([]int64, m+1)
	sufVal[m] = inf
	for i := m - 1; i >= 0; i-- {
		v := segs[i].start + segs[i].cost
		if v <= sufVal[i+1] { // 相等时取更早的分段起点，保证出发时刻最小
			sufVal[i], sufDep[i] = v, segs[i].start
		} else {
			sufVal[i], sufDep[i] = sufVal[i+1], sufDep[i+1]
		}
	}
	return &segList{segs: segs, sufVal: sufVal, sufDep: sufDep}
}

// arrDep 求 arr_e(t) = min{ t'+cost(t') : t' >= t 且 t' 不在封闭段 }，
// 以及取得该最小值的最小出发时刻 dep。每个分段内 t'+cost 随 t' 递增，
// 故最优出发点只可能是 t 本身（t 落在开放段内时）或某个后续开放段的起点；
// 全部后续分段封闭时该边在 t 之后不可用（ok 为 false）。
func (sl *segList) arrDep(t int64) (arr, dep int64, ok bool) {
	// i 为最后一个 start <= t 的分段下标（不存在则为 -1）。
	i := sort.Search(len(sl.segs), func(j int) bool { return sl.segs[j].start > t }) - 1
	arr, dep = inf, inf
	next := i + 1
	if i >= 0 && sl.segs[i].end > t { // t 落在开放段内，可于 t 立即出发
		arr, dep = t+sl.segs[i].cost, t
	}
	if sl.sufVal[next] < arr { // 相等时保留更小的出发时刻 t
		arr, dep = sl.sufVal[next], sl.sufDep[next]
	}
	return arr, dep, arr < inf
}

type pqItem struct {
	dist int64
	node int
}

type pqHeap []pqItem

func (h pqHeap) Len() int { return len(h) }
func (h pqHeap) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist < h[j].dist
	}
	return h[i].node < h[j].node // 确定性的平局打破，保证重放结果逐字一致
}
func (h pqHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pqHeap) Push(x any)   { *h = append(*h, x.(pqItem)) }
func (h *pqHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// query 是一次查询的上下文，按边懒缓存展开后的开放分段。
type query struct {
	nw    *Network
	ver   int64
	dist  []int64
	cache map[int]*segList
}

func (q *query) segListOf(eid int) *segList {
	sl, ok := q.cache[eid]
	if !ok {
		sl = buildSegList(q.nw.edges[eid-1], q.ver)
		q.cache[eid] = sl
	}
	return sl
}

// EarliestArrival 求从 s 在时刻 t0 出发（每个节点后可任意等待）到 g 的
// 最早到达时刻与确定路线。ver 缺省为当前版本；给定 ver 时只使用登记版本
// 不大于 ver 的边与（在该版本下未被替换的）记录，因此同一 ver 的查询
// 结果不会因之后的任何操作而改变。
func (nw *Network) EarliestArrival(s int, t0 int64, g int, vers ...int64) (Result, error) {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	ver := nw.version
	if len(vers) > 0 {
		ver = vers[0]
	}
	if s < 0 || s >= nw.n || g < 0 || g >= nw.n || t0 < 0 || t0 > MaxTime || ver < 0 {
		return Result{}, &OpError{Op: "EarliestArrival", Code: ErrInvalidParam,
			Msg: fmt.Sprintf("s=%d g=%d t0=%d ver=%d 越界", s, g, t0, ver)}
	}
	if ver > nw.version {
		return Result{}, &OpError{Op: "EarliestArrival", Code: ErrVersionNotYet,
			Msg: fmt.Sprintf("查询版本 %d 大于当前版本 %d", ver, nw.version)}
	}
	q := &query{nw: nw, ver: ver, dist: make([]int64, nw.n), cache: make(map[int]*segList)}
	for i := range q.dist {
		q.dist[i] = inf
	}
	q.dist[s] = t0
	settled := make([]bool, nw.n)
	pq := &pqHeap{{dist: t0, node: s}}
	popped := 0
	// 允许任意等待时 arr_e(t) 关于 t 非降（FIFO），Dijkstra 适用；
	// g 被确定后立即停止，popped 不超过 d(x) <= d(g) 的节点个数。
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if settled[it.node] {
			continue
		}
		settled[it.node] = true
		popped++
		if it.node == g {
			break
		}
		for _, eid := range nw.adj[it.node] {
			e := nw.edges[eid-1]
			if e.RegVersion > ver {
				continue
			}
			arr, _, ok := q.segListOf(eid).arrDep(it.dist)
			if !ok {
				continue
			}
			if arr < q.dist[e.V] {
				q.dist[e.V] = arr
				heap.Push(pq, pqItem{dist: arr, node: e.V})
			}
		}
	}
	if q.dist[g] == inf {
		return Result{}, &OpError{Op: "EarliestArrival", Code: ErrUnreachable,
			Msg: fmt.Sprintf("从节点 %d 出发无法到达节点 %d", s, g)}
	}
	return Result{Arrival: q.dist[g], Route: q.buildRoute(s, g, settled), popped: popped}, nil
}

type predEdge struct {
	node, edge int
}

// buildRoute 在紧边（arr_e(d(a)) == d(b)）组成的 DAG 上取 s→g 路线：
// 先边数最少，再边编号序列字典序最小。字典序用逐层 rank 压缩比较，
// 避免存储与逐元素比较完整边序列。
func (q *query) buildRoute(s, g int, settled []bool) []Leg {
	if s == g {
		return nil
	}
	n := q.nw.n
	order := make([]int, 0, n)
	for v := 0; v < n; v++ {
		if settled[v] {
			order = append(order, v)
		}
	}
	sort.Slice(order, func(i, j int) bool { return q.dist[order[i]] < q.dist[order[j]] })
	// 紧边上到达时刻严格递增（耗时 >= 1），按 d 升序即拓扑序。
	tight := func(u, eid int) (int, bool) {
		e := q.nw.edges[eid-1]
		if e.RegVersion > q.ver || !settled[e.V] {
			return 0, false
		}
		arr, _, ok := q.segListOf(eid).arrDep(q.dist[u])
		if !ok || arr != q.dist[e.V] {
			return 0, false
		}
		return e.V, true
	}
	hops := make([]int, n)
	for i := range hops {
		hops[i] = -1
	}
	hops[s] = 0
	for _, u := range order {
		if hops[u] < 0 {
			continue
		}
		for _, eid := range q.nw.adj[u] {
			if v, ok := tight(u, eid); ok && (hops[v] < 0 || hops[u]+1 < hops[v]) {
				hops[v] = hops[u] + 1
			}
		}
	}
	if hops[g] < 0 {
		return nil // 不可达，调用方已排除
	}
	preds := make([][]predEdge, n)
	for _, u := range order {
		if hops[u] < 0 {
			continue
		}
		for _, eid := range q.nw.adj[u] {
			if v, ok := tight(u, eid); ok && hops[v] == hops[u]+1 {
				preds[v] = append(preds[v], predEdge{node: u, edge: eid})
			}
		}
	}
	levels := make([][]int, hops[g]+1)
	for _, u := range order {
		if hops[u] >= 0 && hops[u] <= hops[g] {
			levels[hops[u]] = append(levels[hops[u]], u)
		}
	}
	type seqKey struct{ rank, edge int }
	rank := make([]int, n)
	keys := make([]seqKey, n)
	parentNode := make([]int, n)
	parentEdge := make([]int, n)
	for lv := 1; lv <= hops[g]; lv++ {
		for _, v := range levels[lv] {
			best := seqKey{rank: math.MaxInt, edge: math.MaxInt}
			for _, p := range preds[v] {
				k := seqKey{rank: rank[p.node], edge: p.edge}
				if k.rank < best.rank || (k.rank == best.rank && k.edge < best.edge) {
					best = k
					parentNode[v] = p.node
					parentEdge[v] = p.edge
				}
			}
			keys[v] = best
		}
		lvNodes := levels[lv]
		sort.Slice(lvNodes, func(a, b int) bool {
			ka, kb := keys[lvNodes[a]], keys[lvNodes[b]]
			return ka.rank < kb.rank || (ka.rank == kb.rank && ka.edge < kb.edge)
		})
		r := 0
		for i, v := range lvNodes {
			if i > 0 && keys[v] != keys[lvNodes[i-1]] {
				r++
			}
			rank[v] = r
		}
	}
	var rev []Leg
	for cur := g; cur != s; {
		u, eid := parentNode[cur], parentEdge[cur]
		_, dep, _ := q.segListOf(eid).arrDep(q.dist[u])
		rev = append(rev, Leg{EdgeID: eid, Depart: dep, Arrive: q.dist[cur]})
		cur = u
	}
	route := make([]Leg, len(rev))
	for i := range rev {
		route[len(rev)-1-i] = rev[i]
	}
	return route
}
