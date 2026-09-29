package lag

import (
	"fmt"
	"sort"
)

// Recompute 对给定行做批量重算：每个分区独立按 (SortKey, ID) 升序排列，
// 首行前驱为空，其余行前驱为紧邻其前行的取值。该函数不依赖任何已有状态，
// 作为增量视图的正确性基准。
func Recompute(rows []Row) map[string][]RowView {
	byPart := map[string][]Row{}
	for _, r := range rows {
		byPart[r.Partition] = append(byPart[r.Partition], r)
	}
	out := make(map[string][]RowView, len(byPart))
	for part, rs := range byPart {
		sort.Slice(rs, func(i, j int) bool {
			return lessKey(rs[i].SortKey, rs[i].ID, rs[j].SortKey, rs[j].ID)
		})
		views := make([]RowView, 0, len(rs))
		for i, r := range rs {
			v := RowView{Row: r}
			if i > 0 {
				v.HasPrev, v.Prev = true, rs[i-1].Value
			}
			views = append(views, v)
		}
		out[part] = views
	}
	return out
}

// Verify 对引擎做自检：将当前全部行批量重算，并与增量视图逐分区、逐标识
// 比对（行序、前驱是否为空、前驱取值）。一致时返回 nil，可与提交及其他
// 视图调用并发执行。
func (e *Engine) Verify() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	rows := make([]Row, 0, e.total)
	for _, partRows := range e.parts {
		for _, en := range partRows {
			rows = append(rows, en.row)
		}
	}
	want := Recompute(rows)
	if len(want) != len(e.parts) {
		return fmt.Errorf("lag: verify partition count mismatch: got %d want %d", len(e.parts), len(want))
	}
	for part, wantRows := range want {
		gotRows := e.viewLocked(part)
		if len(gotRows) != len(wantRows) {
			return fmt.Errorf("lag: verify partition %q row count mismatch: got %d want %d",
				part, len(gotRows), len(wantRows))
		}
		for i := range wantRows {
			g, w := gotRows[i], wantRows[i]
			if g.Row != w.Row || g.HasPrev != w.HasPrev || (g.HasPrev && g.Prev != w.Prev) {
				return fmt.Errorf("lag: verify mismatch in partition %q at position %d: got %+v want %+v",
					part, i, g, w)
			}
		}
	}
	return nil
}

// AllRows 返回引擎当前所有行的副本，常用于测试中与 Recompute 对照。
func (e *Engine) AllRows() []Row {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rows := make([]Row, 0, e.total)
	for _, partRows := range e.parts {
		for _, en := range partRows {
			rows = append(rows, en.row)
		}
	}
	return rows
}
