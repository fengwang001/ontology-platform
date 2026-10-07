package traversal

// NaiveTraverse 是独立维护全部路径祖先关系的朴素参照实现：
// 对每条递归路径直接传递并线性扫描祖先切片，逻辑刻意写得直白、
// 与优化实现的哈希集合互相独立，用于在随机图上逐条路径对照。
func NaiveTraverse(snap *snapshot, req TraversalRequest) (*TraversalResult, error) {
	if err := validateRequest(snap, req); err != nil {
		return nil, err
	}
	n := &naiveRunner{
		snap:      snap,
		req:       req,
		pathNodes: []ObjectID{req.Start},
	}
	n.dfs(req.Start, 0)
	n.stats.AncestorChecks = n.stats.CandidateEdges
	return &TraversalResult{
		SnapshotVersion: snap.version,
		Paths:           n.paths,
		Stats:           n.stats,
	}, nil
}

// naiveRunner 是刻意与 traverser 独立的参照实现：
// 不使用任何哈希集合，环路判定通过对本条路径祖先切片的线性扫描完成，
// 且每次判定记录真实发生的比较次数（AncestorProbes），
// 直观展示「朴素做法随路径长度线性增长」与优化做法的区别。
type naiveRunner struct {
	snap      *snapshot
	req       TraversalRequest
	paths     []*TraversedPath
	pathNodes []ObjectID
	pathLinks []TraversedLink
	stats     Stats
}

func (n *naiveRunner) dfs(node ObjectID, depth int) {
	if depth == n.req.MaxDepth {
		n.finish(StatusDepthLimit, nil)
		return
	}
	candidates := enumerateCandidates(n.snap, node, n.req)
	n.stats.ExpandedNodes++
	n.stats.CandidateEdges += len(candidates)
	if len(candidates) == 0 {
		n.finish(StatusBoundary, nil)
		return
	}
	for _, cand := range candidates {
		link := cand.entry.link
		tl := TraversedLink{
			LinkID:    link.ID,
			Type:      link.Type,
			Direction: cand.direction,
			From:      node,
			To:        cand.entry.to,
		}
		// 线性扫描「当前这条路径」的祖先切片。
		idx, found := n.indexOfAncestor(cand.entry.to)
		if found {
			ancSeq := append([]ObjectID(nil), n.pathNodes...)
			n.pathNodes = append(n.pathNodes, cand.entry.to)
			n.pathLinks = append(n.pathLinks, tl)
			n.finish(StatusCycle, &CycleInfo{
				RepeatedObject:   cand.entry.to,
				AncestorIndex:    idx,
				ClosingLink:      tl,
				AncestorSequence: ancSeq,
			})
			n.pathNodes = n.pathNodes[:len(n.pathNodes)-1]
			n.pathLinks = n.pathLinks[:len(n.pathLinks)-1]
			continue
		}
		n.pathNodes = append(n.pathNodes, cand.entry.to)
		n.pathLinks = append(n.pathLinks, tl)
		n.dfs(cand.entry.to, depth+1)
		n.pathLinks = n.pathLinks[:len(n.pathLinks)-1]
		n.pathNodes = n.pathNodes[:len(n.pathNodes)-1]
	}
}

// indexOfAncestor 线性扫描祖先序列并统计比较次数。
// 与遍历实现共享的仅是输入校验和候选枚举两个无关判环的数据准备函数，
// 判环本身（本函数）完全独立。
func (n *naiveRunner) indexOfAncestor(target ObjectID) (int, bool) {
	for i, obj := range n.pathNodes {
		n.stats.AncestorProbes++
		if obj == target {
			return i, true
		}
	}
	return -1, false
}

func (n *naiveRunner) finish(status TerminalStatus, cycle *CycleInfo) {
	nodes := append([]ObjectID(nil), n.pathNodes...)
	links := append([]TraversedLink(nil), n.pathLinks...)
	n.paths = append(n.paths, &TraversedPath{
		Nodes:  nodes,
		Links:  links,
		Depth:  len(links),
		Status: status,
		Cycle:  cycle,
	})
}
