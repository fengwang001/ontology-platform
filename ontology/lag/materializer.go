package lag

import "sort"

// Materializer 是下游按序应用变更日志的视图容器。
//
// 对同一初始状态按日志顺序应用 Insert/Delete 返回的全部 Change，
// 得到的视图与引擎视图（以及批量重算结果）逐分区、逐标识一致。
type Materializer struct {
	rows map[string]map[string]RowView
}

// NewMaterializer 创建空的下游视图。
func NewMaterializer() *Materializer {
	return &Materializer{rows: map[string]map[string]RowView{}}
}

// Apply 按语义应用一条变更：insert/update 写入，delete 删除。
func (m *Materializer) Apply(c Change) {
	part, ok := m.rows[c.Partition]
	if !ok {
		part = map[string]RowView{}
		m.rows[c.Partition] = part
	}
	if c.Kind == ChangeDelete {
		delete(part, c.ID)
		if len(part) == 0 {
			delete(m.rows, c.Partition)
		}
		return
	}
	part[c.ID] = RowView{
		Row:     Row{Partition: c.Partition, ID: c.ID, SortKey: c.SortKey, Value: c.Value},
		HasPrev: c.HasPrev,
		Prev:    c.Prev,
	}
}

// View 返回指定分区内按 (SortKey, ID) 升序排列的物化视图。
func (m *Materializer) View(partition string) []RowView {
	part := m.rows[partition]
	out := make([]RowView, 0, len(part))
	for _, v := range part {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(out[i].Row.SortKey, out[i].Row.ID, out[j].Row.SortKey, out[j].Row.ID)
	})
	return out
}

// Snapshot 返回所有分区的物化视图。
func (m *Materializer) Snapshot() map[string][]RowView {
	out := make(map[string][]RowView, len(m.rows))
	for part := range m.rows {
		out[part] = m.View(part)
	}
	return out
}
