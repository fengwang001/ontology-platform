package ontology

import "sort"

// neighborhood 是一次遍历在某一固定快照、固定参数下的完整邻域结果。
// 它在任何分页开始之前被一次性计算出来，作为固定遍历顺序的权威序列；
// 分页只是对该序列做窗口切片，从而保证续读稳定且可重放。
type neighborhood struct {
	order   []ObjectID
	reason  TruncationReason
	metrics Metrics
}

// buildNeighborhood 在快照上做受限 BFS。
//
// 规则要点：
//   - 权限过滤（对象存在性、链接遍历权）先于扇出判定：不可见对象不占名额；
//   - 每个对象在某一层的后继 = 去重后按对象标识排序的可见对象，只保留前 maxFanout 个；
//   - 被扇出裁掉的后继在任何一页（及续读）中都不出现；
//   - 深度到达 maxDepth 的对象不再扩展；
//   - 截断标记三态，扇出原因优先于深度原因。
func buildNeighborhood(
	snap *Snapshot,
	policy Policy,
	caller *Principal,
	start ObjectID,
	maxDepth int,
	maxFanout int,
) neighborhood {
	m := &Metrics{}
	visited := map[ObjectID]bool{start: true}
	order := []ObjectID{start}
	m.ObjectsVisited++

	fanoutTruncated := false
	depthTruncated := false

	currentLayer := []ObjectID{start}
	for depth := 0; depth < maxDepth && len(currentLayer) > 0; depth++ {
		nextLayerSet := map[ObjectID]bool{}
		var nextLayer []ObjectID

		for _, node := range currentLayer {
			successors, truncated := enumerateSuccessors(snap, policy, caller, node, visited, maxFanout, m)
			if truncated {
				fanoutTruncated = true
			}
			for _, succ := range successors {
				if nextLayerSet[succ] {
					continue
				}
				nextLayerSet[succ] = true
				nextLayer = append(nextLayer, succ)
			}
		}

		// 固定遍历顺序：同层内按对象标识排序，层与层之间按深度先后。
		sort.Slice(nextLayer, func(i, j int) bool { return nextLayer[i] < nextLayer[j] })
		for _, id := range nextLayer {
			visited[id] = true
			order = append(order, id)
			m.ObjectsVisited++
		}
		currentLayer = nextLayer
	}

	// 已到达最大深度的边界对象：若其在无深度限制时本有后继，则构成深度截断。
	// 仅当此前没有发生任何扇出截断时才需要报告（扇出优先级更高）。
	if !fanoutTruncated && maxDepth >= 0 {
		for _, node := range currentLayer {
			if hasSuccessor(snap, policy, caller, node, visited, m) {
				depthTruncated = true
				break
			}
		}
	}

	reason := TruncationNone
	if fanoutTruncated {
		reason = TruncationFanout
	} else if depthTruncated {
		reason = TruncationDepth
	}
	return neighborhood{order: order, reason: reason, metrics: *m}
}

// edgeCursor 是一条从当前节点可沿链接类型方向走出的有向边游标。
type edgeCursor struct {
	link   Link
	target ObjectID
}

// edgeStream 把节点的出/入邻接表按链接类型方向过滤后，得到按目标 ID 有序的边序列。
// 邻接表在存储提交时已排序，因此这里只需线性过滤，两个序列再做归并。
type edgeStream struct {
	links  []Link
	pos    int
	from   ObjectID
	invert bool // true 表示来自入边索引，目标为 from 端
	snap   *Snapshot
}

func newOutStream(snap *Snapshot, node ObjectID) *edgeStream {
	return &edgeStream{links: snap.outgoing[node], from: node, snap: snap}
}

func newInStream(snap *Snapshot, node ObjectID) *edgeStream {
	return &edgeStream{links: snap.incoming[node], from: node, invert: true, snap: snap}
}

func (st *edgeStream) next() (edgeCursor, bool) {
	for st.pos < len(st.links) {
		l := st.links[st.pos]
		st.pos++
		dir := st.snap.linkType(l.Type).Direction
		if st.invert {
			if dir != DirectionIn && dir != DirectionBoth {
				continue
			}
			return edgeCursor{link: l, target: l.From}, true
		}
		if dir != DirectionOut && dir != DirectionBoth {
			continue
		}
		return edgeCursor{link: l, target: l.To}, true
	}
	return edgeCursor{}, false
}

// edgeMerger 归并出/入两个按目标 ID 有序的边序列，输出全局按目标 ID 有序的边。
// 同一对节点间的多条链接（或双向链接在两索引中的镜像）保持稳定次序，
// 由后继去重表负责折叠。
type edgeMerger struct {
	out, in *edgeStream
	a, b    edgeCursor
	ha, hb  bool
}

func newEdgeMerger(snap *Snapshot, node ObjectID) *edgeMerger {
	m := &edgeMerger{out: newOutStream(snap, node), in: newInStream(snap, node)}
	m.a, m.ha = m.out.next()
	m.b, m.hb = m.in.next()
	return m
}

func (m *edgeMerger) next() (edgeCursor, bool) {
	switch {
	case !m.ha && !m.hb:
		return edgeCursor{}, false
	case !m.ha:
		cur := m.b
		m.b, m.hb = m.in.next()
		return cur, true
	case !m.hb:
		cur := m.a
		m.a, m.ha = m.out.next()
		return cur, true
	case m.a.target <= m.b.target:
		cur := m.a
		m.a, m.ha = m.out.next()
		return cur, true
	default:
		cur := m.b
		m.b, m.hb = m.in.next()
		return cur, true
	}
}

// validTarget 应用对象存在性权限与已访问去重，返回目标是否有效。
func filterSuccessorTarget(
	snap *Snapshot,
	policy Policy,
	caller *Principal,
	target ObjectID,
	visited map[ObjectID]bool,
) bool {
	if !policy.CanSeeObject(snap, caller, target) {
		return false
	}
	return !visited[target]
}

// enumerateSuccessors 返回 node 展开后保留的后继（按 ID 排序、最多 maxFanout 个），
// 并报告该节点是否发生了扇出截断。
//
// 权限先于扇出：链接遍历权与对象存在性过滤先于名额统计，
// 不可见对象绝不挤占名额。邻接表按目标 ID 有序，因此只需扫描到
// 第 maxFanout+1 个“有效后继”即可确定截断，扫描量为 O(fanout)，
// 与节点总度数及图的总体规模无关。
func enumerateSuccessors(
	snap *Snapshot,
	policy Policy,
	caller *Principal,
	node ObjectID,
	visited map[ObjectID]bool,
	maxFanout int,
	m *Metrics,
) ([]ObjectID, bool) {
	unique := map[ObjectID]bool{}
	var successors []ObjectID

	merger := newEdgeMerger(snap, node)
	for {
		e, ok := merger.next()
		if !ok {
			break
		}
		m.LinksExamined++
		if !policy.CanTraverseLink(snap, caller, e.link) {
			continue
		}
		if !filterSuccessorTarget(snap, policy, caller, e.target, visited) || unique[e.target] {
			continue
		}
		unique[e.target] = true
		successors = append(successors, e.target)
		if len(successors) == maxFanout+1 {
			break
		}
	}

	if len(successors) > maxFanout {
		return successors[:maxFanout], true
	}
	return successors, false
}

// hasSuccessor 判断 node 是否至少存在一个有效后继（用于深度截断判定）。
func hasSuccessor(
	snap *Snapshot,
	policy Policy,
	caller *Principal,
	node ObjectID,
	visited map[ObjectID]bool,
	m *Metrics,
) bool {
	merger := newEdgeMerger(snap, node)
	for {
		e, ok := merger.next()
		if !ok {
			break
		}
		m.LinksExamined++
		if !policy.CanTraverseLink(snap, caller, e.link) {
			continue
		}
		if filterSuccessorTarget(snap, policy, caller, e.target, visited) {
			return true
		}
	}
	return false
}
