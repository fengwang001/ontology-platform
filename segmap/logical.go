package segmap

// extents 始终按 logicalStart 排序、互不重叠且左闭右开。

// splitExtentsAt 保证某条区段恰好从 at 开始（at 落在区段内部时劈开）。
func splitExtentsAt(es []extent, at int64) []extent {
	i, ok := findExtent(es, at)
	if !ok {
		return es
	}
	if es[i].logicalStart == at {
		return es
	}
	e := es[i]
	cut := at - e.logicalStart
	left := extent{logicalStart: e.logicalStart, physicalStart: e.physicalStart, length: cut}
	right := extent{logicalStart: at, physicalStart: e.physicalStart + cut, length: e.length - cut}
	es = append(es, extent{})
	copy(es[i+1:], es[i:])
	es[i], es[i+1] = left, right
	return es
}

// findExtent 返回包含 at 的区段下标（at 位于其左闭右开区间内）。
func findExtent(es []extent, at int64) (int, bool) {
	lo, hi := 0, len(es)
	for lo < hi {
		mid := (lo + hi) / 2
		switch {
		case at < es[mid].logicalStart:
			hi = mid
		case at >= es[mid].logicalEnd():
			lo = mid + 1
		default:
			return mid, true
		}
	}
	return lo, false
}

// findExtentAtOrAfter 返回第一个 logicalEnd > at 的区段下标。
func findExtentAtOrAfter(es []extent, at int64) int {
	lo, hi := 0, len(es)
	for lo < hi {
		mid := (lo + hi) / 2
		if es[mid].logicalEnd() <= at {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// releaseRange 删除/截断与 [start, end) 重叠的区段，
// 返回被移除映射覆盖的物理子区间（逐段、待 release）。
// 部分重叠的旧区段被劈开、未覆盖部分原样保留。
func releaseRange(es []extent, start, end int64) ([]extent, []extent) {
	if end <= start {
		return es, nil
	}
	es = splitExtentsAt(es, start)
	es = splitExtentsAt(es, end)
	from := findExtentAtOrAfter(es, start)
	to := from
	var removed []extent
	for to < len(es) && es[to].logicalStart < end {
		removed = append(removed, es[to])
		to++
	}
	es = append(es[:from], es[to:]...)
	return es, removed
}

// insertExtent 插入一条区段；调用前必须保证与已有区段不重叠
// （通常先 releaseRange）。插入后合并逻辑与物理都相接的相邻区段。
func insertExtent(es []extent, e extent) []extent {
	pos := findExtentAtOrAfter(es, e.logicalStart)
	es = append(es, extent{})
	copy(es[pos+1:], es[pos:])
	es[pos] = e
	return mergeExtents(es)
}

// mergeExtents 合并同时满足“逻辑端点相接”且“物理端点相接”的相邻区段。
// 仅逻辑相接但物理不相接（共享拷贝造成）时保持独立，保证映射表示唯一。
func mergeExtents(es []extent) []extent {
	if len(es) < 2 {
		return es
	}
	out := es[:1]
	for i := 1; i < len(es); i++ {
		last := &out[len(out)-1]
		cur := es[i]
		logicallyAdjacent := last.logicalEnd() == cur.logicalStart
		physicallyAdjacent := last.physicalStart+last.length == cur.physicalStart
		if logicallyAdjacent && physicallyAdjacent {
			last.length += cur.length
			continue
		}
		out = append(out, cur)
	}
	return out
}

// intersectExtents 收集 es 与 [start, end) 相交的部分，
// 输出区段的逻辑/物理起点都已换算到相交片段上。空洞（无区段）被跳过。
func intersectExtents(es []extent, start, end int64) []extent {
	var out []extent
	i := findExtentAtOrAfter(es, start)
	for ; i < len(es); i++ {
		e := es[i]
		if e.logicalStart >= end {
			break
		}
		lo := max64(e.logicalStart, start)
		hi := min64(e.logicalEnd(), end)
		off := lo - e.logicalStart
		out = append(out, extent{
			logicalStart:  lo,
			physicalStart: e.physicalStart + off,
			length:        hi - lo,
		})
	}
	return out
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
