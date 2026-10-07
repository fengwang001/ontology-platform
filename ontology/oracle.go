package ontology

import "sort"

// oracleGraph 是独立于生产实现的朴素图表示：所有链接装在一个无序列表里。
// 对照模型刻意采用“穷举扫描”写法（每次展开都扫全部链接、全量排序），
// 与生产代码的有序邻接表+提前终止共享尽量少的实现，以最大化交叉验证价值。
type oracleGraph struct {
	objects map[ObjectID]bool
	links   []oracleLink
}

type oracleLink struct {
	typ      LinkTypeID
	dir      LinkDirection
	from, to ObjectID
}

// oracleFromSnapshot 把某一快照导出为朴素图（仅测试对照使用）。
func oracleFromSnapshot(snap *Snapshot) *oracleGraph {
	g := &oracleGraph{objects: map[ObjectID]bool{}}
	for id := range snap.objects {
		g.objects[id] = true
	}
	for _, links := range snap.outgoing {
		for _, l := range links {
			g.links = append(g.links, oracleLink{
				typ:  l.Type,
				dir:  snap.linkType(l.Type).Direction,
				from: l.From,
				to:   l.To,
			})
		}
	}
	return g
}

// OracleResult 是朴素模型的输出。
type OracleResult struct {
	Order  []ObjectID
	Reason TruncationReason
}

// oracleNeighborhood 以朴素穷举方式计算受限 BFS 邻域。
// 语义必须与 buildNeighborhood 完全一致，包括：
//   - 权限先于扇出；
//   - 同层按对象标识排序、全局首次出现去重；
//   - maxFanout==0 时仍可扩展但不保留任何后继；
//   - 扇出截断优先于深度截断。
func oracleNeighborhood(
	g *oracleGraph,
	policy Policy,
	snap *Snapshot,
	caller *Principal,
	start ObjectID,
	maxDepth int,
	maxFanout int,
) OracleResult {
	seen := map[ObjectID]bool{start: true}
	order := []ObjectID{start}
	fanoutCut := false
	depthCut := false

	layer := []ObjectID{start}
	for d := 0; d < maxDepth && len(layer) > 0; d++ {
		var next []ObjectID
		nextSet := map[ObjectID]bool{}
		for _, node := range layer {
			succ, cut := oracleExpand(g, policy, snap, caller, node, seen, maxFanout)
			fanoutCut = fanoutCut || cut
			for _, id := range succ {
				if !nextSet[id] {
					nextSet[id] = true
					next = append(next, id)
				}
			}
		}
		sortObjectIDs(next)
		for _, id := range next {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
		layer = next
	}

	if !fanoutCut {
		for _, node := range layer {
			if oracleHasAny(g, policy, snap, caller, node, seen) {
				depthCut = true
				break
			}
		}
	}

	reason := TruncationNone
	if fanoutCut {
		reason = TruncationFanout
	} else if depthCut {
		reason = TruncationDepth
	}
	return OracleResult{Order: order, Reason: reason}
}

// oracleExpand 穷举扫描全部链接，朴素地收集一个节点的全部有效后继后排序截断。
func oracleExpand(
	g *oracleGraph,
	policy Policy,
	snap *Snapshot,
	caller *Principal,
	node ObjectID,
	seen map[ObjectID]bool,
	maxFanout int,
) ([]ObjectID, bool) {
	set := map[ObjectID]bool{}
	var all []ObjectID
	for _, l := range g.links {
		var target ObjectID
		switch {
		case l.from == node && (l.dir == DirectionOut || l.dir == DirectionBoth):
			target = l.to
		case l.to == node && (l.dir == DirectionIn || l.dir == DirectionBoth):
			target = l.from
		default:
			continue
		}
		link := Link{Type: l.typ, From: l.from, To: l.to}
		if !policy.CanTraverseLink(snap, caller, link) {
			continue
		}
		if target == node || seen[target] || set[target] {
			continue
		}
		if !policy.CanSeeObject(snap, caller, target) {
			continue
		}
		set[target] = true
		all = append(all, target)
	}
	sortObjectIDs(all)
	if len(all) > maxFanout {
		return append([]ObjectID(nil), all[:maxFanout]...), true
	}
	return all, false
}

func oracleHasAny(
	g *oracleGraph,
	policy Policy,
	snap *Snapshot,
	caller *Principal,
	node ObjectID,
	seen map[ObjectID]bool,
) bool {
	for _, l := range g.links {
		var target ObjectID
		switch {
		case l.from == node && (l.dir == DirectionOut || l.dir == DirectionBoth):
			target = l.to
		case l.to == node && (l.dir == DirectionIn || l.dir == DirectionBoth):
			target = l.from
		default:
			continue
		}
		if !policy.CanTraverseLink(snap, caller, Link{Type: l.typ, From: l.from, To: l.to}) {
			continue
		}
		if target != node && !seen[target] && policy.CanSeeObject(snap, caller, target) {
			return true
		}
	}
	return false
}

func sortObjectIDs(ids []ObjectID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
