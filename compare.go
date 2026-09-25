package ontology

// scoreLess 在忽略浮点符号位差异（+0.0 与 -0.0 相等）的前提下
// 比较两个合法（非 NaN）分数。
func scoreLess(a, b float64) bool {
	if a == 0 && b == 0 {
		return false
	}
	return a < b
}

// rankLess 是 Snapshot 使用的复合次序：
// 方向只作用于 Score 一维；Score 相等（含 +0/-0）时一律按 ID 字典序升序。
func rankLess(dir Direction, a, b Element) bool {
	same := a.Score == b.Score
	if !same && a.Score == 0 && b.Score == 0 {
		same = true
	}
	if same {
		return a.ID < b.ID
	}
	if dir == Desc {
		return b.Score < a.Score
	}
	return scoreLess(a.Score, b.Score)
}

// heapLess 是内部堆的次序：堆顶为当前保留集合中"最差"的元素。
func heapLess(dir Direction, a, b Element) bool {
	same := a.Score == b.Score
	if !same && a.Score == 0 && b.Score == 0 {
		same = true
	}
	if same {
		return a.ID > b.ID
	}
	if dir == Desc {
		return scoreLess(a.Score, b.Score)
	}
	return b.Score < a.Score
}
