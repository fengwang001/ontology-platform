package traversal

import "sort"

// validateRequest 按固定次序校验请求，只返回第一类错误：
// 1) 起始对象不存在；
// 2) 方向集合为空、方向值非法、或链接类型未登记；
// 3) 深度上限非正整数。
//
// 校验在已获取的开始快照上进行，拒绝的请求不会执行任何扩展，
// 因而不消耗遍历资源、也不产生任何部分结果。
func validateRequest(snap *snapshot, req TraversalRequest) error {
	if _, ok := snap.objects[req.Start]; !ok {
		return ErrStartObjectNotFound
	}
	if len(req.Directions) == 0 {
		return ErrInvalidDirections
	}
	for t, dir := range req.Directions {
		if _, ok := snap.linkTypes[t]; !ok {
			return ErrInvalidDirections
		}
		if !dir.isValid() {
			return ErrInvalidDirections
		}
	}
	if req.MaxDepth <= 0 {
		return ErrInvalidDepth
	}
	return nil
}

// candidate 是一跳的扩展候选。平行链接在此被展开为彼此独立的候选，
// 因而同一起点上的不同链接各自独立参与后续判定。
type candidate struct {
	entry     adjacencyEntry
	direction Direction
}

// enumerateCandidates 按确定顺序枚举节点在允许方向集合上的全部候选链接。
// 同一条链接在 DirBoth 下会产生入、出两个方向的候选（若两方向均被请求）。
func enumerateCandidates(snap *snapshot, node ObjectID, req TraversalRequest) []candidate {
	var out []candidate
	types := make([]LinkTypeID, 0, len(req.Directions))
	for t := range req.Directions {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	for _, t := range types {
		dir := req.Directions[t]
		if dir == DirOutbound || dir == DirBoth {
			for _, e := range snap.outgoing[node] {
				if e.link.Type == t {
					out = append(out, candidate{entry: e, direction: DirOutbound})
				}
			}
		}
		if dir == DirInbound || dir == DirBoth {
			for _, e := range snap.incoming[node] {
				if e.link.Type == t {
					out = append(out, candidate{entry: e, direction: DirInbound})
				}
			}
		}
	}
	// 邻接表内部已按 (类型, 目标, 链接ID) 排序；DirBoth 下同一链接的
	// 出、入两个候选也通过方向次序保持全局确定的扩展顺序。
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.entry.link.Type != b.entry.link.Type {
			return a.entry.link.Type < b.entry.link.Type
		}
		if a.direction != b.direction {
			return a.direction < b.direction
		}
		if a.entry.to != b.entry.to {
			return a.entry.to < b.entry.to
		}
		return a.entry.link.ID < b.entry.link.ID
	})
	return out
}

// traverser 持有单次遍历的可变执行状态。
//
// 关键设计：祖先判定严格使用「当前这一条递归路径」自己的 ancestors
// （map + 切片），绝不用任何全局已访问集合替代。进入递归时加入、
// 回溯时移除，使菱形汇聚（不同分支到达同一节点）不会被误判为环路。
// ancestors map 令每次归属核对为 O(1)，不随路径长度或总图规模增长。
type traverser struct {
	snap      *snapshot
	req       TraversalRequest
	paths     []*TraversedPath
	stats     Stats
	ancestors map[ObjectID]int // 对象 -> 其在 pathNodes 中的下标
	pathNodes []ObjectID
	pathLinks []TraversedLink
}

func traverse(snap *snapshot, req TraversalRequest) (*TraversalResult, error) {
	if err := validateRequest(snap, req); err != nil {
		return nil, err
	}
	snap.ensureIndexes()
	t := &traverser{
		snap:      snap,
		req:       req,
		ancestors: map[ObjectID]int{req.Start: 0},
		pathNodes: []ObjectID{req.Start},
	}
	t.dfs(req.Start, 0)
	return &TraversalResult{
		SnapshotVersion: snap.version,
		Paths:           t.paths,
		Stats:           t.stats,
	}, nil
}

func (t *traverser) dfs(node ObjectID, depth int) {
	// 已处于深度上限：该路径被截断。即便节点仍有可扩展候选也不再枚举，
	// 因为那些候选将落在深度 MaxDepth+1；但注意进入本节点之前的一跳
	// 已经先做过环路判定，故「同跳既是环路又触顶」的情形已在调用方
	// 固定报告为环路，不会到达这里。
	if depth == t.req.MaxDepth {
		t.finish(StatusDepthLimit, nil)
		return
	}

	candidates := enumerateCandidates(t.snap, node, t.req)
	t.stats.ExpandedNodes++
	t.stats.CandidateEdges += len(candidates)
	t.stats.AncestorChecks += len(candidates)

	if len(candidates) == 0 {
		t.finish(StatusBoundary, nil)
		return
	}

	for _, cand := range candidates {
		link := cand.entry.link
		to := cand.entry.to
		tl := TraversedLink{
			LinkID:    link.ID,
			Type:      link.Type,
			Direction: cand.direction,
			From:      node,
			To:        to,
		}

		// 环路判定先于一切（包括深度判定），且为每条候选链接独立进行：
		// 自环（to == node）在第一跳即命中；平行链接中的一条构成环路
		// 不影响同层其余链接各自继续。
		t.stats.AncestorProbes++
		if idx, cyclic := t.ancestors[to]; cyclic {
			ancSeq := append([]ObjectID(nil), t.pathNodes...)
			t.pathNodes = append(t.pathNodes, to)
			t.pathLinks = append(t.pathLinks, tl)
			cycle := &CycleInfo{
				RepeatedObject:   to,
				AncestorIndex:    idx,
				ClosingLink:      tl,
				AncestorSequence: ancSeq,
			}
			t.finish(StatusCycle, cycle)
			t.pathNodes = t.pathNodes[:len(t.pathNodes)-1]
			t.pathLinks = t.pathLinks[:len(t.pathLinks)-1]
			continue
		}

		// 非环路：多路径重复到达也照常进入递归。to 可能在另一条
		// 不相交路径上到达过，但只要不在本条路径的祖先序列中就继续扩展。
		t.ancestors[to] = len(t.pathNodes)
		t.pathNodes = append(t.pathNodes, to)
		t.pathLinks = append(t.pathLinks, tl)
		t.dfs(to, depth+1)
		t.pathLinks = t.pathLinks[:len(t.pathLinks)-1]
		t.pathNodes = t.pathNodes[:len(t.pathNodes)-1]
		delete(t.ancestors, to)
	}
}

// finish 固化当前递归路径为一条结果路径。
func (t *traverser) finish(status TerminalStatus, cycle *CycleInfo) {
	nodes := append([]ObjectID(nil), t.pathNodes...)
	links := append([]TraversedLink(nil), t.pathLinks...)
	t.paths = append(t.paths, &TraversedPath{
		Nodes:  nodes,
		Links:  links,
		Depth:  len(links),
		Status: status,
		Cycle:  cycle,
	})
}
