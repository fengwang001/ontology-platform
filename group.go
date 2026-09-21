package ontology

import "sort"

// group 保存一个分组的当前 Top-N（按 less 升序排列，最优在前）
// 以及该组的跳过计数。rows 长度永不超过 n。
type group struct {
	rows    []Row
	skipped int64 // 分数缺失或非数值的行数
	nan     int64 // 分数为 NaN 的行数
}

// add 尝试把 r 插入组内 Top-N；若组已满且 r 不优于当前最后一名则丢弃。
// 调用方必须持有选择器的锁。
func (g *group) add(r Row, n int) {
	if len(g.rows) == n && !less(&r, &g.rows[n-1]) {
		return
	}
	pos := sort.Search(len(g.rows), func(i int) bool {
		return less(&r, &g.rows[i])
	})
	g.rows = append(g.rows, Row{})
	copy(g.rows[pos+1:], g.rows[pos:])
	g.rows[pos] = r
	if len(g.rows) > n {
		g.rows = g.rows[:n]
	}
}

// snapshot 返回组内行的独立拷贝（含 Fields map 的拷贝）。
func (g *group) snapshot() []Row {
	out := make([]Row, len(g.rows))
	for i, r := range g.rows {
		fields := make(map[string]any, len(r.Fields))
		for k, v := range r.Fields {
			fields[k] = v
		}
		out[i] = Row{Fields: fields, Score: r.Score, Tie: r.Tie, id: r.id}
	}
	return out
}
