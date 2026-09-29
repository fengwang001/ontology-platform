package lag

import "sort"

// RowView 是对外可见的一行视图：行本体加上其前驱取值。
// HasPrev 为 false 表示前驱严格为空（分区首行），与 Value/Prev 为零值
// 的情形严格区分。
type RowView struct {
	Row
	HasPrev bool
	Prev    string
}

// Snapshot 返回某分区当前按 (SortKey, ID) 升序排列的行视图。
// 分区不存在时返回 (nil, false)。
func (e *Engine) Snapshot(partition string) ([]RowView, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rows, ok := e.parts[partition]
	if !ok {
		return nil, false
	}
	return orderedView(rows), true
}

// Rows 返回某分区当前按 (SortKey, ID) 升序排列的行视图（分区不存在时为空切片）。
func (e *Engine) Rows(partition string) []RowView {
	v, _ := e.Snapshot(partition)
	return v
}

// Partitions 返回当前所有非空分区名，按字典序排列。
func (e *Engine) Partitions() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.parts))
	for name := range e.parts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AllSnapshot 返回所有分区的视图，按分区名、分区内 (SortKey, ID) 双升序排列。
func (e *Engine) AllSnapshot() []RowView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.parts))
	for name := range e.parts {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []RowView
	for _, name := range names {
		out = append(out, orderedView(e.parts[name])...)
	}
	return out
}

// orderedView 调用方须持有读锁或写锁。
func orderedView(rows map[string]*entry) []RowView {
	ens := make([]*entry, 0, len(rows))
	for _, en := range rows {
		ens = append(ens, en)
	}
	sort.Slice(ens, func(i, j int) bool {
		return lessKey(ens[i].row.SortKey, ens[i].row.ID, ens[j].row.SortKey, ens[j].row.ID)
	})
	out := make([]RowView, len(ens))
	for i, en := range ens {
		v := RowView{Row: en.row}
		if i > 0 {
			v.HasPrev, v.Prev = true, ens[i-1].row.Value
		}
		out[i] = v
	}
	return out
}
