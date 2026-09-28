package reachability

import (
	"io"
	"sort"
	"sync"
)

// MaxEdgeMultiplicity 是单条有向边允许的最大重数。
const MaxEdgeMultiplicity uint64 = 1 << 32

// Graph 并发安全地维护有向图边重数与全量可达点对。
//
// 并发模型：一把互斥锁串行化所有增删操作与版本指针切换；读取
// （Reachable / Snapshot / ReachablePairs）只取一次不可变快照指针，
// 不加锁即可遍历，因此与写入并发时读到的一定是某个已提交版本的
// 完整点对集合（逐点对一致），绝不会读到半成品状态。
type Graph struct {
	mu sync.Mutex

	// edges：有向边重数；边存在当且仅当重数 > 0。
	edges map[Pair]uint64
	// out/in：仅包含重数为正的边的正/逆向邻接。
	out map[string]map[string]struct{}
	in  map[string]map[string]struct{}
	// current：当前发布的不可变可达快照。
	current *Snapshot
	// log：输入、可达集合与判定依据日志；nil 表示不打印。
	log *logger
}

// Option 配置新创建的 Graph。
type Option func(*Graph)

// WithLogger 将操作日志写到 w（传入 nil 等同关闭日志）。
func WithLogger(w io.Writer) Option {
	return func(g *Graph) { g.log = newLogger(w) }
}

// New 创建空图。
func New(opts ...Option) *Graph {
	g := &Graph{
		edges:   map[Pair]uint64{},
		out:     map[string]map[string]struct{}{},
		in:      map[string]map[string]struct{}{},
		current: emptySnapshot(),
		log:     newLogger(nil),
	}
	for _, opt := range opts {
		opt(g)
	}
	if g.log == nil {
		g.log = newLogger(nil)
	}
	return g
}

// AddEdge 增加一条重数为 1 的有向边。
func (g *Graph) AddEdge(from, to string) (*ChangeResult, error) {
	return g.AddEdgeN(from, to, 1)
}

// RemoveEdge 删除一条重数为 1 的有向边。
func (g *Graph) RemoveEdge(from, to string) (*ChangeResult, error) {
	return g.RemoveEdgeN(from, to, 1)
}

// AddEdgeN 将 (from -> to) 的重数增加 n（n 必须为正）。
func (g *Graph) AddEdgeN(from, to string, n uint64) (*ChangeResult, error) {
	if err := validateNames("add_edge", from, to); err != nil {
		g.log.logRejected("add_edge", from, to, n, err)
		return nil, err
	}
	if n == 0 {
		err := newOpError(KindInvalidArgument, "add_edge", from, to,
			"edge count must be a positive number")
		g.log.logRejected("add_edge", from, to, n, err)
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	before := g.current
	old := g.edges[Pair{from, to}]
	if n > MaxEdgeMultiplicity || old > MaxEdgeMultiplicity-n {
		err := newOpError(KindMultiplicityOverflow, "add_edge", from, to,
			"adding would exceed MaxEdgeMultiplicity")
		g.log.logRejected("add_edge", from, to, n, err)
		return nil, err
	}

	edge := Pair{from, to}
	structureChanged := old == 0
	if structureChanged {
		addAdj(g.out, from, to)
		addAdj(g.in, to, from)
	}
	g.edges[edge] = old + n

	var added []PairWitness
	if structureChanged {
		added = g.applyAdd(from, to)
	}
	after := g.current

	res := &ChangeResult{
		Op:                 "add_edge",
		Edge:               edge,
		MultiplicityBefore: old,
		MultiplicityAfter:  old + n,
		StructureChanged:   structureChanged,
		AddedPairs:         added,
		Before:             before,
		After:              after,
	}
	g.log.logChange(res)
	return res, nil
}

// RemoveEdgeN 将 (from -> to) 的重数减少 n（n 必须为正，且边当前必须存在）。
func (g *Graph) RemoveEdgeN(from, to string, n uint64) (*ChangeResult, error) {
	if err := validateNames("remove_edge", from, to); err != nil {
		g.log.logRejected("remove_edge", from, to, n, err)
		return nil, err
	}
	if n == 0 {
		err := newOpError(KindInvalidArgument, "remove_edge", from, to,
			"edge count must be a positive number")
		g.log.logRejected("remove_edge", from, to, n, err)
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	edge := Pair{from, to}
	old, ok := g.edges[edge]
	if !ok || old == 0 {
		err := newOpError(KindEdgeNotFound, "remove_edge", from, to,
			"cannot remove an edge whose multiplicity is zero")
		g.log.logRejected("remove_edge", from, to, n, err)
		return nil, err
	}
	if n > old {
		err := newOpError(KindEdgeNotFound, "remove_edge", from, to,
			"cannot remove more copies than the current multiplicity")
		g.log.logRejected("remove_edge", from, to, n, err)
		return nil, err
	}

	before := g.current
	structureChanged := n == old // 减量后重数归零，边真正消失
	var removed []Pair
	var restored []PairWitness
	if structureChanged {
		delete(g.edges, edge)
		removeAdj(g.out, from, to)
		removeAdj(g.in, to, from)
		removed, restored = g.applyRemove(from, to)
	} else {
		g.edges[edge] = old - n
	}
	after := g.current

	res := &ChangeResult{
		Op:                 "remove_edge",
		Edge:               edge,
		MultiplicityBefore: old,
		MultiplicityAfter:  old - n,
		StructureChanged:   structureChanged,
		RemovedPairs:       removed,
		RestoredPairs:      restored,
		Before:             before,
		After:              after,
	}
	g.log.logChange(res)
	return res, nil
}

// validateNames 拒绝空节点名（在触碰任何状态之前调用）。
func validateNames(op, from, to string) error {
	if from == "" || to == "" {
		return newOpError(KindEmptyNodeName, op, from, to,
			"node names must be non-empty")
	}
	return nil
}

// applyAdd 把所有“经由新边 x->y”才成立的可达点对并入当前快照。
//
// 设旧图中 A = 可达 x 的点集（含 x），B = 从 y 可达的点集（含 y），
// 则新可达点对恰为 A × B 中此前尚未记录的点对（自环情形自动覆盖）。
// 见证路径统一构造为 “旧图中到 x 的路径 + 新边 + 旧图中从 y 的路径”，
// 与操作结果同序、确定可复现。
func (g *Graph) applyAdd(x, y string) []PairWitness {
	reach := g.current.clone()

	// A：旧图中可达 x 的点，加入 x 自身（x 可能经环已可达自身，需去重）。
	A := uniqueSorted(append([]string{x}, g.current.Sources(x)...))

	// B：旧图中从 y 可达的点，加入 y 自身（同理去重）。
	B := uniqueSorted(append([]string{y}, g.current.ReachableFrom(y)...))

	oldAdj := g.adjacencyForWitnessesBefore(x, y)

	var added []PairWitness
	for _, u := range A {
		dests, exists := reach[u]
		if !exists {
			dests = map[string]struct{}{}
			reach[u] = dests
		}
		var prefix []string
		if u != x {
			prefix = bfsPath(u, x, oldAdj) // 旧图中的见证路径
		}
		for _, v := range B {
			if _, already := dests[v]; already {
				continue
			}
			dests[v] = struct{}{}
			var path []string
			switch {
			case u == x && v == y:
				path = []string{x, y}
			case u == x:
				path = append([]string{x, y}, bfsPath(y, v, oldAdj)[1:]...)
			case v == y:
				path = append(append([]string{}, prefix...), y)
			default:
				path = append(append(append([]string{}, prefix...), y),
					bfsPath(y, v, oldAdj)[1:]...)
			}
			added = append(added, PairWitness{Pair: Pair{u, v}, Path: path})
		}
	}
	g.current = newSnapshot(reach)

	sort.Slice(added, func(i, j int) bool {
		if added[i].From != added[j].From {
			return added[i].From < added[j].From
		}
		return added[i].To < added[j].To
	})
	return added
}

// adjacencyForWitnessesBefore 构造“加边之前”的邻接：
// 在当前（已含新边）邻接上临时删掉 x->y，使见证路径只引用旧路径。
func (g *Graph) adjacencyForWitnessesBefore(x, y string) map[string]map[string]struct{} {
	adj := make(map[string]map[string]struct{}, len(g.out))
	for u, ns := range g.out {
		cp := make(map[string]struct{}, len(ns))
		for v := range ns {
			cp[v] = struct{}{}
		}
		adj[u] = cp
	}
	if ns, ok := adj[x]; ok {
		delete(ns, y)
		if len(ns) == 0 {
			delete(adj, x)
		}
	}
	return adj
}

// applyRemove 实现删边的“先移除、再推导”：
//  1. 在删除前的图上求 Pred(x)={可达 x 的点} 与 Succ(y)={从 y 可达的点}；
//     任何经过被删边 x->y 的路径，其起点必在 Pred(x)、终点必在 Succ(y)，
//     故可能受影响的点对不会超出 Pred(x) × Succ(y)（宁可过删，绝不漏删）；
//  2. 把这些点对从可达集合中全部移除；
//  3. 在删除后的图上朴素遍历，把仍然可达的点对连同新见证路径重新加回。
//
// 先移除保证任何只靠被删边成立的可达性立即消失；朴素重算保证
// 存在替代路径的点对一定被恢复——既不过删也不漏算。
func (g *Graph) applyRemove(x, y string) (removed []Pair, restored []PairWitness) {
	oldReach := g.current.clone()

	// 调用时正/逆向邻接已删掉 x->y，临时加回以还原删除前的图。
	beforeAdj := withTemporaryEdge(g.out, x, y)

	// 步骤 1：圈定可能受影响的点对（含 x、y 自身）。
	predSet := bfsReachReverse(x, beforeAdj)
	predSet[x] = struct{}{}
	succSet := bfsReach(y, beforeAdj)
	succSet[y] = struct{}{}

	candidates := map[Pair]struct{}{}
	for u := range predSet {
		for v := range succSet {
			// 仅移除删除前确实可达的点对（Pred(x)×Succ(y) 在删除前
			// 理论上均可达，这里以旧快照为准做防御性过滤）。
			if dests, ok := oldReach[u]; ok {
				if _, reachableBefore := dests[v]; reachableBefore {
					candidates[Pair{u, v}] = struct{}{}
				}
			}
		}
	}

	// 步骤 2：全部移除候选点对。
	next := make(map[string]map[string]struct{}, len(oldReach))
	for u, dests := range oldReach {
		cp := make(map[string]struct{}, len(dests))
		for v := range dests {
			if _, hit := candidates[Pair{u, v}]; hit {
				continue
			}
			cp[v] = struct{}{}
		}
		if len(cp) > 0 {
			next[u] = cp
		}
	}

	// 步骤 3：在删除后的真实图上朴素遍历，重新推导。
	afterNodes := nodeUniverse(g.out, g.in)
	fresh := fullClosure(afterNodes, g.out)

	for _, u := range afterNodes {
		target, ok := next[u]
		if !ok {
			target = map[string]struct{}{}
		}
		for _, v := range sortedSet(fresh[u]) {
			if _, wasRemoved := candidates[Pair{u, v}]; wasRemoved {
				// 被移除后仍然可达：恢复并给出删边后图上的新见证路径。
				target[v] = struct{}{}
				restored = append(restored, PairWitness{
					Pair: Pair{u, v},
					Path: witnessAfterRemoval(u, v, g.out),
				})
			} else if _, present := target[v]; !present {
				// 未受影响点对必然仍成立；保守补全以防漏算。
				target[v] = struct{}{}
			}
		}
		if len(target) > 0 {
			next[u] = target
		} else {
			delete(next, u)
		}
	}

	// 被移除但未恢复的点对，才是真正消失的可达性。
	restoredSet := map[Pair]struct{}{}
	for _, w := range restored {
		restoredSet[w.Pair] = struct{}{}
	}
	for p := range candidates {
		if _, back := restoredSet[p]; !back {
			removed = append(removed, p)
		}
	}

	g.current = newSnapshot(next)

	sort.Slice(removed, func(i, j int) bool {
		if removed[i].From != removed[j].From {
			return removed[i].From < removed[j].From
		}
		return removed[i].To < removed[j].To
	})
	sort.Slice(restored, func(i, j int) bool {
		if restored[i].From != restored[j].From {
			return restored[i].From < restored[j].From
		}
		return restored[i].To < restored[j].To
	})
	return removed, restored
}

// witnessAfterRemoval 在删边后的图上取一条最短见证路径。
func witnessAfterRemoval(u, v string, adj map[string]map[string]struct{}) []string {
	if u == v {
		if p := shortestCycleFrom(u, adj); p != nil {
			return p
		}
		return []string{u}
	}
	return bfsPath(u, v, adj)
}

// withTemporaryEdge 返回在 adj 上加入 x->y 后的副本（不修改入参）。
func withTemporaryEdge(adj map[string]map[string]struct{}, x, y string) map[string]map[string]struct{} {
	cp := make(map[string]map[string]struct{}, len(adj)+1)
	for u, ns := range adj {
		m := make(map[string]struct{}, len(ns)+1)
		for v := range ns {
			m[v] = struct{}{}
		}
		cp[u] = m
	}
	addAdj(cp, x, y)
	return cp
}

func addAdj(adj map[string]map[string]struct{}, u, v string) {
	ns, ok := adj[u]
	if !ok {
		ns = map[string]struct{}{}
		adj[u] = ns
	}
	ns[v] = struct{}{}
}

func removeAdj(adj map[string]map[string]struct{}, u, v string) {
	if ns, ok := adj[u]; ok {
		delete(ns, v)
		if len(ns) == 0 {
			delete(adj, u)
		}
	}
}

func sortedSet(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func uniqueSorted(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Reachable 判定当前版本下 from 是否可达 to（路径长度 >= 1）。
// 空节点名返回 KindEmptyNodeName 错误且不改变任何状态。
//
// 结论与作为判定依据的见证路径在同一个锁区间内取得，保证日志中
// 打印的依据与结论来自同一个已提交版本。
func (g *Graph) Reachable(from, to string) (bool, error) {
	if from == "" || to == "" {
		err := newOpError(KindEmptyNodeName, "reachable", from, to,
			"node names must be non-empty")
		g.log.logRejected("reachable", from, to, 0, err)
		return false, err
	}
	g.mu.Lock()
	ok := g.current.Reachable(from, to)
	var witness []string
	if ok {
		if from == to {
			witness = shortestCycleFrom(from, g.out)
		} else {
			witness = bfsPath(from, to, g.out)
		}
	}
	g.mu.Unlock()
	g.log.logReachable(from, to, ok, witness)
	return ok, nil
}

// Snapshot 返回当前不可变可达快照。返回后即使图继续变更，
// 该快照内容保持不变，可被任意数量 goroutine 并发读取。
func (g *Graph) Snapshot() *Snapshot {
	g.mu.Lock()
	s := g.current
	g.mu.Unlock()
	return s
}

// ReachablePairs 按字典序返回当前全部可达点对。
func (g *Graph) ReachablePairs() []Pair {
	return g.Snapshot().Pairs()
}
