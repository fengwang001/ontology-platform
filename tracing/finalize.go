package tracing

import "sort"

// node 为组装期间的树节点。
type node struct {
	span     Span
	shift    int64 // 累计平移量
	children []*node
}

func (n *node) start() int64 { return n.span.Start + n.shift }
func (n *node) end() int64   { return n.span.End + n.shift }

// finalize 把一个追踪的全部暂存跨度组装为完结输出。
// 输出只取决于跨度集合本身，与到达顺序无关。
func (a *Assembler) finalize(traceID string, ts *traceState) TraceResult {
	spans := make([]Span, 0, len(ts.spans))
	for _, s := range ts.spans {
		spans = append(spans, s)
	}
	sortSpans(spans)

	byID := make(map[string]*node, len(spans))
	var root *node
	for _, s := range spans {
		nd := &node{span: s}
		byID[s.SpanID] = nd
		if s.ParentID == "" {
			root = nd
		}
	}

	// 判定每个跨度沿父链向上是否能到达根：不能到达（无父或成环）者为孤儿。
	var orphans []Span
	inTree := make(map[string]bool, len(spans))
	for _, s := range spans {
		if s.ParentID == "" {
			inTree[s.SpanID] = true
			continue
		}
		if reachRoot(s.SpanID, byID, root) {
			inTree[s.SpanID] = true
		} else {
			orphans = append(orphans, s)
			a.log("orphan trace=%s span=%s parent=%q (无父或父子成环)", traceID, s.SpanID, s.ParentID)
		}
	}

	// 挂接树中节点，子节点按跨度号排序保证确定性。
	for _, s := range spans {
		if !inTree[s.SpanID] || s.ParentID == "" {
			continue
		}
		parent := byID[s.ParentID]
		parent.children = append(parent.children, byID[s.SpanID])
	}
	for _, nd := range byID {
		sort.Slice(nd.children, func(i, j int) bool {
			return nd.children[i].span.SpanID < nd.children[j].span.SpanID
		})
	}

	// 自根向下逐层校正跨服务时钟偏移。
	if root != nil {
		a.correctLevel(traceID, root)
	}

	placed := make([]PlacedSpan, 0, len(spans)-len(orphans))
	for _, s := range spans {
		if inTree[s.SpanID] {
			placed = append(placed, PlacedSpan{Span: s, Shift: byID[s.SpanID].shift})
		}
	}
	sort.Slice(placed, func(i, j int) bool { return placed[i].Span.SpanID < placed[j].Span.SpanID })
	sortSpans(orphans)

	var critical []string
	if root != nil {
		critical = criticalPath(root)
	}

	a.log("finalize trace=%s spans=%d orphans=%d critical=%v", traceID, len(placed), len(orphans), critical)
	return TraceResult{
		TraceID:      traceID,
		Spans:        placed,
		Orphans:      orphans,
		CriticalPath: critical,
	}
}

// reachRoot 判断从 spanID 沿父链向上能否到达根（不成环、父均存在）。
func reachRoot(spanID string, byID map[string]*node, root *node) bool {
	seen := make(map[string]bool)
	cur := spanID
	for {
		if seen[cur] {
			return false // 成环
		}
		seen[cur] = true
		nd, ok := byID[cur]
		if !ok {
			return false // 父不存在
		}
		if nd == root {
			return true
		}
		if nd.span.ParentID == "" {
			return false
		}
		cur = nd.span.ParentID
	}
}

// correctLevel 自根向下逐层处理：对当前层每个节点，先校正其跨服务子跨度，
// 再对下一层递归，使异服务后代相对已平移的父重新判定。
func (a *Assembler) correctLevel(traceID string, parent *node) {
	for _, child := range parent.children {
		if child.span.Service != parent.span.Service {
			a.shiftChild(traceID, parent, child)
		}
	}
	for _, child := range parent.children {
		a.correctLevel(traceID, child)
	}
}

// shiftChild 计算并应用子跨度的最小平移量，平移范围为该子跨度连同
// 其子树中不经过其他服务的同服务后代。
func (a *Assembler) shiftChild(traceID string, parent, child *node) {
	cs, ce := child.start(), child.end()
	ps, pe := parent.start(), parent.end()
	if cs >= ps && ce <= pe {
		return // 子区间已落在父区间内
	}
	var delta int64
	if ce-cs > pe-ps {
		delta = ps - cs // 子比父长，起点对齐
	} else if cs < ps {
		delta = ps - cs // 整体早于父，向右平移
	} else {
		delta = pe - ce // 整体晚于父，向左平移
	}
	a.log("shift trace=%s span=%s svc=%s child=[%d,%d] parent=[%d,%d] delta=%d",
		traceID, child.span.SpanID, child.span.Service, cs, ce, ps, pe, delta)
	applyShift(child, delta)
}

// applyShift 把 delta 应用于 nd 及其子树中不经过其他服务的同服务后代。
// 继续向下只平移与 nd 同服务的节点，遇到异服务节点即停止
// （它们将相对已平移的父重新判定）。
func applyShift(nd *node, delta int64) {
	nd.shift += delta
	for _, c := range nd.children {
		if c.span.Service == nd.span.Service {
			applyShift(c, delta)
		}
	}
}

// criticalPath 从根起每层选（平移后）结束最晚的子跨度，并列取跨度号最小者。
func criticalPath(root *node) []string {
	path := []string{root.span.SpanID}
	cur := root
	for len(cur.children) > 0 {
		best := cur.children[0]
		for _, c := range cur.children[1:] {
			if c.end() > best.end() ||
				(c.end() == best.end() && c.span.SpanID < best.span.SpanID) {
				best = c
			}
		}
		path = append(path, best.span.SpanID)
		cur = best
	}
	return path
}
