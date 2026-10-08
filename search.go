package ontology

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// queryMetrics 是单次查询的内部度量，只统计本次查询实际访问过的对象与
// 链接数目。它用于验证查询开销与起点终点邻域规模相当、与整图规模无关；
// 属于内部实现细节，不向查询者暴露。
type queryMetrics struct {
	// ObjectsVisited 是搜索过程中弹出（展开）的路径节点数。
	ObjectsVisited int
	// LinksVisited 是搜索过程中检查过的邻接边条目数。
	LinksVisited int
}

// pathNode 是最佳优先搜索队列中的一条部分路径。
type pathNode struct {
	obj     ObjectID
	objs    []ObjectID
	links   []LinkID
	cats    []Category
	cost    int64
	state   []int
	visited map[ObjectID]struct{}
}

// compareCategorySeq 按类别集合的固定全序做字典序比较；互为前缀时短者更小。
func compareCategorySeq(rank map[Category]int, a, b []Category) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		ra, rb := rank[a[i]], rank[b[i]]
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// compareObjectSeq 按对象标识做字典序比较；互为前缀时短者更小。
func compareObjectSeq(a, b []ObjectID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// compareLinkSeq 按链接标识做字典序比较，用于等价最优路径中选取代表。
func compareLinkSeq(a, b []LinkID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// comparePriority 实现候选路径的全序：先总代价，再类别序列字典序，
// 最后对象标识序列字典序。
func comparePriority(rank map[Category]int, a, b *pathNode) int {
	if a.cost != b.cost {
		if a.cost < b.cost {
			return -1
		}
		return 1
	}
	if c := compareCategorySeq(rank, a.cats, b.cats); c != 0 {
		return c
	}
	return compareObjectSeq(a.objs, b.objs)
}

// pathHeap 是按 comparePriority 排序的最小堆。
type pathHeap struct {
	nodes []*pathNode
	rank  map[Category]int
}

func (h *pathHeap) Len() int { return len(h.nodes) }
func (h *pathHeap) Less(i, j int) bool {
	return comparePriority(h.rank, h.nodes[i], h.nodes[j]) < 0
}
func (h *pathHeap) Swap(i, j int) { h.nodes[i], h.nodes[j] = h.nodes[j], h.nodes[i] }
func (h *pathHeap) Push(x any)    { h.nodes = append(h.nodes, x.(*pathNode)) }
func (h *pathHeap) Pop() any {
	old := h.nodes
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	h.nodes = old[:n-1]
	return it
}

// domKey 是支配剪枝的键：终点对象、自动机状态与已访问对象集合都相同的
// 两条部分路径，未来可接的续段完全相同。
type domKey struct {
	obj     ObjectID
	state   string
	visited string
}

func visitedKey(visited map[ObjectID]struct{}) string {
	ids := make([]string, 0, len(visited))
	for id := range visited {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x1f")
}

// Query 执行一次跨类型最短路径查询。判定次序为：参数非法 > 起点或终点
// 所在对象类型被禁止 > 不可达（合法结果）。查询期间持有读锁，因此本次
// 查询看到的图状态与权限可见性是某一时刻的完整快照。
func (g *Graph) Query(q PathQuery) (PathResult, error) {
	res, _, err := g.query(q)
	return res, err
}

// query 是 Query 的内部版本，额外返回内部度量，供实现内部验证使用。
func (g *Graph) query(q PathQuery) (PathResult, queryMetrics, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// 1. 参数非法判定先于一切图上搜索。
	cp, err := compilePattern(q.Pattern, g.catRank)
	if err != nil {
		return PathResult{}, queryMetrics{}, err
	}
	startObj, ok := g.objects[q.Start]
	if !ok {
		return PathResult{}, queryMetrics{}, fmt.Errorf("%w: start object %q not found", ErrInvalidParams, q.Start)
	}
	endObj, ok := g.objects[q.End]
	if !ok {
		return PathResult{}, queryMetrics{}, fmt.Errorf("%w: end object %q not found", ErrInvalidParams, q.End)
	}

	// 2. 起点或终点所在对象类型被禁止参与路径查询。
	if g.objectTypes[startObj.typ].ForbiddenInPathQuery || g.objectTypes[endObj.typ].ForbiddenInPathQuery {
		return PathResult{}, queryMetrics{}, fmt.Errorf("%w: endpoint object type %q/%q",
			ErrForbiddenObjectType, startObj.typ, endObj.typ)
	}

	// 3. 图上搜索；不可达是合法结果而非错误。
	res, m := g.search(q.Start, q.End, cp, q.Principal)
	return res, m, nil
}

// search 在（已持有读锁的）图上做最佳优先搜索。
//
// 搜索按 (总代价, 类别序列, 对象序列) 的全序弹出部分路径。任意候选路径
// 的真前缀在该全序下严格更小（代价不增、类别序列是真前缀），因此第一个
// 被弹出的接受路径就是全序最小的候选路径；与其优先级完全相同的其余接受
// 路径此刻必然已经全部入堆，可一次性收集为等价组。
func (g *Graph) search(start, end ObjectID, cp *compiledPattern, principal Principal) (PathResult, queryMetrics) {
	var m queryMetrics

	// 起点与终点相同：任何非空路径都会重复经过起点，唯一可能的候选是
	// 长度为零的路径，仅当约束序列允许匹配空序列时成立。
	if start == end {
		if cp.accepting(cp.start()) {
			return PathResult{Found: true, Objects: []ObjectID{start}}, m
		}
		return PathResult{}, m
	}

	h := &pathHeap{rank: g.catRank}
	heap.Push(h, &pathNode{
		obj:     start,
		objs:    []ObjectID{start},
		state:   cp.start(),
		visited: map[ObjectID]struct{}{start: {}},
	})
	dom := make(map[domKey]*pathNode)

	var best *pathNode
	var winners []*pathNode
	for h.Len() > 0 {
		n := heap.Pop(h).(*pathNode)
		m.ObjectsVisited++
		if best != nil {
			// 已找到最优：优先级严格更大的子树不可能产生同级或更优解。
			if comparePriority(g.catRank, n, best) > 0 {
				break
			}
			// 优先级完全相同的节点：若也是接受路径则属于等价组；
			// 否则其后代优先级必然严格更大，直接丢弃。
			if n.obj == end && cp.accepting(n.state) {
				winners = append(winners, n)
			}
			continue
		}
		if n.obj == end {
			if cp.accepting(n.state) {
				best = n
				winners = append(winners, n)
			}
			// 终点不得作为中间节点（继续扩展必然重复经过终点）。
			continue
		}
		for _, l := range g.out[n.obj] {
			m.LinksVisited++
			v := otherEnd(l, n.obj)
			vo := g.objects[v]
			// 检查顺序固定：先隔离/禁止（对象级），后权限（链接级）。
			if v != end {
				if vo.isolated {
					continue
				}
				if g.objectTypes[vo.typ].ForbiddenInPathQuery {
					continue
				}
			}
			if !l.typ.visibleTo(principal) {
				continue
			}
			if _, seen := n.visited[v]; seen {
				continue
			}
			ns := cp.step(n.state, l.typ.spec.Category)
			if len(ns) == 0 {
				continue
			}
			child := &pathNode{
				obj:   v,
				objs:  append(append([]ObjectID(nil), n.objs...), v),
				links: append(append([]LinkID(nil), n.links...), l.id),
				cats:  append(append([]Category(nil), n.cats...), l.typ.spec.Category),
				cost:  n.cost + l.typ.spec.Cost,
				state: ns,
			}
			child.visited = make(map[ObjectID]struct{}, len(n.visited)+1)
			for id := range n.visited {
				child.visited[id] = struct{}{}
			}
			child.visited[v] = struct{}{}

			// 支配剪枝：终点对象、自动机状态、已访问集合都相同且优先级
			// 严格更小的已有路径，使本条路径的任何续段都严格更差。
			// 优先级完全相等的路径（仅链接实例不同）必须保留以判定等价。
			key := domKey{obj: v, state: stateKey(ns), visited: visitedKey(child.visited)}
			if prev, ok := dom[key]; ok {
				c := comparePriority(g.catRank, prev, child)
				if c < 0 {
					continue
				}
				if c > 0 {
					dom[key] = child
				}
			} else {
				dom[key] = child
			}
			heap.Push(h, child)
		}
	}

	if best == nil {
		return PathResult{}, m
	}
	// 等价组内选择链接标识序列最小者作为确定性的代表，不随机选择。
	canonical := best
	for _, w := range winners[1:] {
		if compareLinkSeq(w.links, canonical.links) < 0 {
			canonical = w
		}
	}
	return PathResult{
		Found:      true,
		Equivalent: len(winners) > 1,
		Cost:       canonical.cost,
		Objects:    canonical.objs,
		Links:      canonical.links,
		Categories: canonical.cats,
	}, m
}
