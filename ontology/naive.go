package ontology

// NaiveTraverse 是独立维护的朴素参考实现：
// 在完整图上逐链接枚举所有深度受限游走（无论链接是否可见），环判定
// 基于完整图祖先序列；再按调用方权限做可见性投影与聚合。
// 测试中用它与 Service.Traverse 在大量随机场景上逐条比对结果。
func NaiveTraverse(snap *Snapshot, req TraverseRequest) *TraverseResponse {
	resp := &TraverseResponse{SnapshotVersion: snap.version}
	if _, ok := snap.objects[req.Start]; !ok {
		return resp
	}
	if len(req.Labels) == 0 || req.MaxDepth <= 0 {
		return resp
	}
	if !objectVisible(snap, req.Start, req.Labels) {
		return resp
	}

	n := &naive{
		snap:           snap,
		labels:         req.Labels,
		hiddenKind:     map[string]HiddenKind{},
		hiddenPrefix:   map[string][]Hop{},
		hiddenFrontier: map[string]string{},
	}
	n.enumerate(req.Start, map[string]int{req.Start: 0}, req.MaxDepth)

	for key, kind := range n.hiddenKind {
		frontier := n.hiddenFrontier[key]
		verdict := VerdictPartialInvisible
		if kind == HiddenCycle {
			verdict = VerdictHiddenCycle
		}
		n.results = append(n.results, PathResult{
			Prefix:  cloneHops(n.hiddenPrefix[key]),
			End:     frontier,
			Verdict: verdict,
			Hidden:  kind,
		})
	}

	resp.Paths = dedupPaths(n.results)
	return resp
}

type naive struct {
	snap           *Snapshot
	labels         map[string]struct{}
	results        []PathResult
	hiddenKind     map[string]HiddenKind
	hiddenPrefix   map[string][]Hop
	hiddenFrontier map[string]string
}

// visState 是仅沿可见链接的可见游走状态。
type visState struct {
	node      string
	visible   []Hop
	ancestors map[string]int
	remaining int
}

// hidState 是进入隐藏区后的一条完整图游走状态；其祖先集合为
// “可见前沿节点 + 已走隐藏路径”，与可见游走的其它分支无关。
type hidState struct {
	node      string
	frontier  string
	prefix    []Hop
	ancestors map[string]int
	remaining int
}

func (n *naive) enumerate(start string, initial map[string]int, maxDepth int) {
	visStack := []visState{{node: start, ancestors: initial, remaining: maxDepth}}
	for len(visStack) > 0 {
		cur := visStack[len(visStack)-1]
		visStack = visStack[:len(visStack)-1]

		var visOut, hidOut []*Link
		for _, lk := range n.snap.out[cur.node] {
			if _, ok := n.labels[lk.Label]; ok {
				visOut = append(visOut, lk)
			} else {
				hidOut = append(hidOut, lk)
			}
		}

		// 存在不可见出边：先登记 PARTIAL；隐藏区探索若发现环则升级为 CYCLE。
		// 剩余跳数为 0 时，隐藏边本身无法在额度内闭合（闭合还需一跳），
		// 故只报 PARTIAL，不探索。
		if cur.remaining > 0 && len(hidOut) > 0 {
			n.recordHidden(cur.node, cur.visible, HiddenPartial)
			for _, lk := range hidOut {
				n.exploreHidden(lk, cur)
			}
		}

		// 深度边界：有隐藏边 => PARTIAL（额度内无法闭合）；
		// 无隐藏边 => 非环路径。
		if cur.remaining == 0 {
			// 环判定优先于深度：边界处的可见边若闭合祖先仍属真实环。
			for _, lk := range visOut {
				if _, cyclic := cur.ancestors[lk.To]; cyclic {
					n.results = append(n.results, PathResult{
						Prefix: appendVisible(cur.visible, lk), End: lk.To,
						Verdict: VerdictVisibleCycle, Hidden: HiddenNone,
					})
				}
			}
			if len(hidOut) > 0 && len(cur.visible) > 0 {
				n.recordHidden(cur.node, cur.visible, HiddenPartial)
			} else if len(cur.visible) > 0 {
				n.results = append(n.results, PathResult{
					Prefix: cloneHops(cur.visible), End: cur.node,
					Verdict: VerdictNonCyclic, Hidden: HiddenNone,
				})
			}
			continue
		}

		// 完全可见死路（且无隐藏延伸）：正常返回非环路径。
		// 有隐藏出边时，该前沿仅由隐藏聚合标记表达。
		if len(visOut) == 0 && len(hidOut) == 0 && len(cur.visible) > 0 {
			n.results = append(n.results, PathResult{
				Prefix: cloneHops(cur.visible), End: cur.node,
				Verdict: VerdictNonCyclic, Hidden: HiddenNone,
			})
		}

		for _, lk := range visOut {
			if _, cyclic := cur.ancestors[lk.To]; cyclic {
				n.results = append(n.results, PathResult{
					Prefix: appendVisible(cur.visible, lk), End: lk.To,
					Verdict: VerdictVisibleCycle, Hidden: HiddenNone,
				})
				continue
			}
			nextAncestors := cloneAncestors(cur.ancestors)
			nextAncestors[lk.To] = len(cur.visible) + 1
			visStack = append(visStack, visState{
				node:      lk.To,
				visible:   appendVisible(cur.visible, lk),
				ancestors: nextAncestors,
				remaining: cur.remaining - 1,
			})
		}
	}
}

// exploreHidden 从首条不可见链接 first 开始，在完整图（忽略标签）上
// 做深度受限环判定；闭合到隐藏祖先集合即 CYCLE。
func (n *naive) exploreHidden(first *Link, from visState) {
	anc := cloneAncestors(from.ancestors) // 含可见前沿节点
	if _, cyclic := anc[first.To]; cyclic {
		// 首条不可见链接直接闭合到可见祖先：真实隐藏环。
		n.recordHidden(from.node, from.visible, HiddenCycle)
		return
	}
	anc[first.To] = len(anc)
	stack := []hidState{{
		node: first.To, frontier: from.node, prefix: from.visible,
		ancestors: anc, remaining: from.remaining - 1,
	}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, lk := range n.snap.out[cur.node] {
			if _, cyclic := cur.ancestors[lk.To]; cyclic {
				n.recordHidden(cur.frontier, cur.prefix, HiddenCycle)
				return
			}
			if cur.remaining == 0 {
				continue
			}
			next := cloneAncestors(cur.ancestors)
			next[lk.To] = len(next)
			stack = append(stack, hidState{
				node: lk.To, frontier: cur.frontier, prefix: cur.prefix,
				ancestors: next, remaining: cur.remaining - 1,
			})
		}
	}
}

func (n *naive) recordHidden(frontier string, prefix []Hop, kind HiddenKind) {
	key := hopsKey(frontier, prefix)
	if cur, ok := n.hiddenKind[key]; ok && cur == HiddenCycle {
		return
	}
	n.hiddenKind[key] = kind
	n.hiddenPrefix[key] = cloneHops(prefix)
	n.hiddenFrontier[key] = frontier
}

func hopsKey(frontier string, prefix []Hop) string {
	key := frontier + "|"
	for _, h := range prefix {
		key += h.LinkID + ">"
	}
	return key
}

func appendVisible(prefix []Hop, lk *Link) []Hop {
	return append(cloneHops(prefix), Hop{
		LinkID: lk.ID, From: lk.From, To: lk.To, Label: lk.Label,
	})
}
