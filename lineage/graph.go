package lineage

import (
	"context"
	"log/slog"
	"sort"
)

// Edge 表示血缘图中的一条 输入版本 → 输出版本 的有向边。
type Edge struct {
	From      ObjectRef
	To        ObjectRef
	Operation string
	Invalid   bool
}

// Graph 是从某个根对象出发追溯得到的确定性血缘图。
type Graph struct {
	Root  ObjectRef
	Edges []Edge      // 按 (From, To, Operation) 排序，与派生发生顺序无关
	Nodes []ObjectRef // 按 (ID, Version) 排序，包含 Root
}

// adjacency 是在只读快照上构建的双向邻接索引。
type adjacency struct {
	up   map[ObjectRef][]storedEdge // to   -> 生产边
	down map[ObjectRef][]storedEdge // from -> 消费边
}

func (t *Tracker) buildAdjacencyLocked() adjacency {
	adj := adjacency{
		up:   make(map[ObjectRef][]storedEdge),
		down: make(map[ObjectRef][]storedEdge),
	}
	for _, e := range t.state.edges {
		adj.up[e.to] = append(adj.up[e.to], e)
		adj.down[e.from] = append(adj.down[e.from], e)
	}
	less := func(a, b storedEdge) bool {
		if a.from != b.from {
			return refLess(a.from, b.from)
		}
		if a.to != b.to {
			return refLess(a.to, b.to)
		}
		return a.operation < b.operation
	}
	for _, list := range adj.up {
		sort.Slice(list, func(i, j int) bool { return less(list[i], list[j]) })
	}
	for _, list := range adj.down {
		sort.Slice(list, func(i, j int) bool { return less(list[i], list[j]) })
	}
	return adj
}

func refLess(a, b ObjectRef) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Version < b.Version
}

// traceLocked 从 root 沿 next 方向做确定性 BFS。
// direction 为 "up"（向上游）或 "down"（向下游）。
// 图中任何失效边都意味着血缘指向过期版本，返回 ErrStaleVersion。
func (t *Tracker) traceLocked(ctx context.Context, root ObjectRef, adj adjacency, direction string) (*Graph, error) {
	next := adj.down
	other := adj.up
	if direction == "up" {
		next = adj.up
		other = adj.down
	}

	visited := map[ObjectRef]bool{root: true}
	nodeSet := map[ObjectRef]bool{root: true}
	edgeSet := make(map[Edge]bool)
	var edgeList []Edge
	queue := []ObjectRef{root}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, se := range next[cur] {
			neighbor := se.to
			if direction == "up" {
				neighbor = se.from
			}
			edge := Edge{From: se.from, To: se.to, Operation: se.operation, Invalid: se.invalid}
			if !edgeSet[edge] {
				edgeSet[edge] = true
				edgeList = append(edgeList, edge)
			}
			nodeSet[neighbor] = true
			if se.invalid {
				t.logger.LogAttrs(ctx, slog.LevelWarn, "lineage query rejected: stale edge encountered",
					slog.String("direction", direction),
					slog.String("reason", "stale_version"),
					slog.Any("root", root),
					slog.Any("from", se.from),
					slog.Any("to", se.to))
				return nil, ErrStaleVersion
			}
			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}

	_ = other
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].From != edgeList[j].From {
			return refLess(edgeList[i].From, edgeList[j].From)
		}
		if edgeList[i].To != edgeList[j].To {
			return refLess(edgeList[i].To, edgeList[j].To)
		}
		return edgeList[i].Operation < edgeList[j].Operation
	})

	nodes := make([]ObjectRef, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return refLess(nodes[i], nodes[j]) })

	return &Graph{Root: root, Edges: edgeList, Nodes: nodes}, nil
}

// checkRefLocked 校验被查询的版本引用，并返回它是否为外部来源对象。
func (t *Tracker) checkRefLocked(ref ObjectRef) (bool, error) {
	if !ref.valid() {
		return false, ErrInvalidRecord
	}
	cur, known := t.state.current[ref.ID]
	if !known {
		return false, ErrUnknownVersion
	}
	if ref.Version > cur {
		return false, ErrUnknownVersion
	}
	if ref.Version < cur {
		return false, ErrStaleVersion
	}
	return t.state.sources[ref.ID], nil
}

// Upstream 追溯上游：谁产生了 ref。
// 外部来源对象（无生产者）返回空图；派生对象缺失生产血缘则拒绝（ErrNoUpstream）。
func (t *Tracker) Upstream(ctx context.Context, ref ObjectRef) (*Graph, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	source, err := t.checkRefLocked(ref)
	if err != nil {
		t.logQueryReject(ctx, "upstream", ref, err)
		return nil, err
	}
	adj := t.buildAdjacencyLocked()
	producers := adj.up[ref]
	if len(producers) == 0 {
		if source {
			return t.traceLocked(ctx, ref, adj, "up")
		}
		t.logQueryReject(ctx, "upstream", ref, ErrNoUpstream)
		return nil, ErrNoUpstream
	}
	g, err := t.traceLocked(ctx, ref, adj, "up")
	if err != nil {
		return nil, err
	}
	t.logQueryResult(ctx, "upstream", ref, g)
	return g, nil
}

// Downstream 追溯下游：ref 产生了谁。没有任何消费血缘则拒绝（ErrNoDownstream）。
func (t *Tracker) Downstream(ctx context.Context, ref ObjectRef) (*Graph, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if _, err := t.checkRefLocked(ref); err != nil {
		t.logQueryReject(ctx, "downstream", ref, err)
		return nil, err
	}
	adj := t.buildAdjacencyLocked()
	if len(adj.down[ref]) == 0 {
		t.logQueryReject(ctx, "downstream", ref, ErrNoDownstream)
		return nil, ErrNoDownstream
	}
	g, err := t.traceLocked(ctx, ref, adj, "down")
	if err != nil {
		return nil, err
	}
	t.logQueryResult(ctx, "downstream", ref, g)
	return g, nil
}

// Lineage 双向追溯：同时返回上游与下游血缘图。
// 任一侧不完整（漏上游/漏下游/指向过期版本）都会被拒绝。
func (t *Tracker) Lineage(ctx context.Context, ref ObjectRef) (up, down *Graph, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	source, err := t.checkRefLocked(ref)
	if err != nil {
		t.logQueryReject(ctx, "both", ref, err)
		return nil, nil, err
	}
	adj := t.buildAdjacencyLocked()
	if len(adj.down[ref]) == 0 {
		t.logQueryReject(ctx, "both", ref, ErrNoDownstream)
		return nil, nil, ErrNoDownstream
	}
	if len(adj.up[ref]) == 0 && !source {
		t.logQueryReject(ctx, "both", ref, ErrNoUpstream)
		return nil, nil, ErrNoUpstream
	}
	up, err = t.traceLocked(ctx, ref, adj, "up")
	if err != nil {
		return nil, nil, err
	}
	down, err = t.traceLocked(ctx, ref, adj, "down")
	if err != nil {
		return nil, nil, err
	}
	t.logQueryResult(ctx, "both-up", ref, up)
	t.logQueryResult(ctx, "both-down", ref, down)
	return up, down, nil
}

func (t *Tracker) logQueryReject(ctx context.Context, direction string, ref ObjectRef, err error) {
	t.logger.LogAttrs(ctx, slog.LevelWarn, "lineage query rejected",
		slog.String("direction", direction),
		slog.Any("ref", ref),
		slog.String("reason", reasonOf(err)))
}

func (t *Tracker) logQueryResult(ctx context.Context, direction string, ref ObjectRef, g *Graph) {
	t.logger.LogAttrs(ctx, slog.LevelInfo, "lineage query ok",
		slog.String("direction", direction),
		slog.Any("ref", ref),
		slog.Int("edges", len(g.Edges)),
		slog.Int("nodes", len(g.Nodes)),
		slog.Any("edges_detail", g.Edges))
}

func reasonOf(err error) string {
	switch err {
	case ErrInvalidRecord:
		return "invalid_record"
	case ErrNoUpstream:
		return "no_upstream"
	case ErrNoDownstream:
		return "no_downstream"
	case ErrStaleVersion:
		return "stale_version"
	case ErrUnknownVersion:
		return "unknown_version"
	default:
		return "unknown"
	}
}
