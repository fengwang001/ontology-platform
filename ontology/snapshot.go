package ontology

import "sort"

// arc 是快照子图上的一条有向可遍历弧（同一条双向链接可展开为两条弧）。
type arc struct {
	linkID string
	from   string
	to     string
}

// visibleSubgraph 是两阶段权限过滤完成后、在整个判定过程中保持不变的快照。
// 它不持有对 Graph 内部 map 的引用，判定期间任何并发变更都不可能影响它，
// 因此每次调用都对应一个确定的串行时刻（持 RLock 期间完成复制与过滤）。
type visibleSubgraph struct {
	// objects 按 ID 排序，是“存在性权限过滤”后的可见对象全集。
	objects []string
	// adjacency[v] 为从 v 出发、按 (to, linkID) 排序的可遍历弧。
	adjacency map[string][]arc
	// selfLoops[v] 记录 v 上的自环链接 ID（长度为 1 的环），按 ID 排序。
	selfLoops map[string][]string
}

// buildSnapshot 严格按题面规定的次序执行过滤：
//
//	阶段 1：剔除调用者无存在性权限的对象（其关联的全部链接随之不参与）；
//	阶段 2：在剩余链接中剔除调用者无遍历权限的链接；
//	之后才在剩余子图上判定环。
//
// 该次序不可颠倒：阶段 1 的存在性过滤会连带动摇链接的端点资格，
// 必须先于遍历权限检查发生（代码中两个阶段是分开的循环）。
//
// 调用本方法时必须持有 g.mu 的读锁。
func (g *Graph) buildSnapshot(caller string) (*visibleSubgraph, Result, Metrics, error) {
	perm := g.permissions[caller].normalized()

	// 阶段 1：存在性权限。仅遍历权限表中授权的对象 ID，不去扫描全图对象，
	// 因而开销只与“可见对象”规模相关，与不可见对象总量无关。
	visibleSet := make(map[string]bool)
	objects := make([]string, 0, len(perm.ExistObject))
	for id := range perm.ExistObject {
		if _, ok := g.objects[id]; ok { // 已删除/不存在的对象授权不算可见
			visibleSet[id] = true
			objects = append(objects, id)
		}
	}
	sort.Strings(objects)

	metrics := Metrics{VisibleObjects: len(objects)}

	// 特殊结果：可见对象集合为空 => 直接无环（不是错误），
	// 且不展开任何链接。
	if len(objects) == 0 {
		return &visibleSubgraph{objects: nil, adjacency: map[string][]arc{}, selfLoops: map[string][]string{}}, Result{HasCycle: false}, metrics, nil
	}

	snap := &visibleSubgraph{
		objects:   objects,
		adjacency: make(map[string][]arc, len(objects)),
		selfLoops: make(map[string][]string),
	}

	// 阶段 2：遍历权限。只枚举与可见对象关联的链接（incident 索引），
	// 完全位于不可见对象之间的链接永远不会被读取。
	seenLinks := make(map[string]bool)
	for _, v := range objects {
		for _, lid := range g.incident[v] {
			if seenLinks[lid] {
				continue
			}
			seenLinks[lid] = true
			metrics.ScannedLinks++

			l := g.links[lid]
			// 阶段 1 的连带效果：端点任一不可见，整条链接视为不存在。
			if !visibleSet[l.Source] || !visibleSet[l.Target] {
				continue
			}
			// 阶段 2：无遍历权限则剔除（即便它是某个环的唯一必要边）。
			if !perm.TraverseLink[lid] {
				continue
			}
			lt := g.linkTypes[l.Type]
			if l.Source == l.Target {
				snap.selfLoops[l.Source] = append(snap.selfLoops[l.Source], lid)
				continue
			}
			snap.adjacency[l.Source] = append(snap.adjacency[l.Source], arc{linkID: lid, from: l.Source, to: l.Target})
			if lt.Direction == Bidirectional {
				snap.adjacency[l.Target] = append(snap.adjacency[l.Target], arc{linkID: lid, from: l.Target, to: l.Source})
			}
		}
	}
	for _, v := range objects {
		arcs := snap.adjacency[v]
		sort.Slice(arcs, func(i, j int) bool {
			if arcs[i].to != arcs[j].to {
				return arcs[i].to < arcs[j].to
			}
			return arcs[i].linkID < arcs[j].linkID
		})
		sort.Strings(snap.selfLoops[v])
	}

	res, dm := snap.detectCycle()
	metrics.VisitedObjects = dm.VisitedObjects
	metrics.VisitedLinks = dm.VisitedLinks
	return snap, res, metrics, nil
}
