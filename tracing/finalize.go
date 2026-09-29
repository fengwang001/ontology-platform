package tracing

import "sort"

// finalize 在追踪完结时构建调用树、校正跨服务时钟偏移并计算关键路径。
// 输出仅取决于跨度集合本身，与到达顺序无关。
func finalize(traceID string, spans map[string]Span) TraceResult {
	children := make(map[string][]string, len(spans))
	attached := make(map[string]struct{}, len(spans))
	var rootID string
	for id, s := range spans {
		if s.ParentID == "" {
			rootID = id
			continue
		}
		if _, ok := spans[s.ParentID]; !ok {
			continue // 父跨度缺失：孤儿
		}
		children[s.ParentID] = append(children[s.ParentID], id)
	}
	for id := range children {
		sort.Strings(children[id])
	}

	// 自根向下 DFS，检测父子成环；不可达的跨度为孤儿。
	offsets := make(map[string]int64, len(spans))
	state := make(map[string]int, len(spans)) // 0=未访问 1=在栈上 2=已完成
	var walk func(id string, base int64)
	walk = func(id string, base int64) {
		state[id] = 1
		attached[id] = struct{}{}
		offsets[id] = base
		s := spans[id]
		for _, cid := range children[id] {
			if state[cid] != 0 {
				continue // 成环：跳过，最终列为孤儿
			}
			child := spans[cid]
			delta := base
			if child.Service != s.Service {
				// 相对已平移的父区间，对子区间（含继承平移）重新判定。
				pStart := s.Start + base
				pEnd := s.End + base
				delta = base + shiftAmount(pStart, pEnd, child.Start+base, child.End+base)
			}
			walk(cid, delta)
		}
		state[id] = 2
	}
	if rootID != "" {
		walk(rootID, 0)
	}

	var orphans []string
	for id := range spans {
		if _, ok := attached[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)

	accepted := make([]AcceptedSpan, 0, len(spans))
	for id, s := range spans {
		accepted = append(accepted, AcceptedSpan{Span: s, Offset: offsets[id]})
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].SpanID < accepted[j].SpanID })

	return TraceResult{
		TraceID:      traceID,
		Spans:        accepted,
		Orphans:      orphans,
		CriticalPath: criticalPath(rootID, spans, children, offsets),
	}
}

// shiftAmount 计算把子区间 [cStart,cEnd] 平移进父区间 [pStart,pEnd] 的
// 最小绝对值平移量；子比父长时改为起点对齐。入参均为已含累计平移的时刻。
func shiftAmount(pStart, pEnd, cStart, cEnd int64) int64 {
	var shift int64
	switch {
	case cStart < pStart:
		shift = pStart - cStart
	case cEnd > pEnd:
		shift = pEnd - cEnd
	}
	if cEnd-cStart > pEnd-pStart {
		shift = pStart - cStart
	}
	return shift
}

// criticalPath 从根起每层选校正后结束最晚的子跨度，并列取跨度号最小者。
func criticalPath(rootID string, spans map[string]Span, children map[string][]string, offsets map[string]int64) []string {
	if rootID == "" {
		return nil
	}
	var pathOut []string
	current := rootID
	for {
		pathOut = append(pathOut, current)
		best := ""
		var bestEnd int64
		for _, cid := range children[current] {
			if _, ok := offsets[cid]; !ok {
				continue // 成环未附着的子跨度不参与
			}
			end := spans[cid].End + offsets[cid]
			if best == "" || end > bestEnd || (end == bestEnd && cid < best) {
				best, bestEnd = cid, end
			}
		}
		if best == "" {
			return pathOut
		}
		current = best
	}
}
