package idx

// NaiveModel 是独立实现的朴素参照模型：保存全部事件，每次查询时
// 从头批量重建索引。它不共享 Store 的任何代码路径，用于在随机操作
// 序列下与 Store 逐条对照。
//
// 语义约定（与 Store 的合法串行顺序一致）：同一 (对象, 依据属性) 上
// 按 (Version, ID) 升序应用事件，最终取值即索引内容。
type NaiveModel struct {
	events []Event
	basis  string
}

func NewNaiveModel(basis string) *NaiveModel {
	return &NaiveModel{basis: basis}
}

// SetBasis 切换参照模型的依据字段（与 Store 切换提交/回滚同步调用）。
func (m *NaiveModel) SetBasis(basis string) {
	m.basis = basis
}

// Add 追加一条事件（允许乱序、重复）。
func (m *NaiveModel) Add(ev Event) {
	m.events = append(m.events, ev)
}

// Query 全量重建后按取值定位对象，返回有序对象 ID 列表。
func (m *NaiveModel) Query(value string) []string {
	winner := make(map[string]lwwEntry)
	for _, ev := range m.events {
		if ev.Property != m.basis {
			continue
		}
		cand := lwwEntry{version: ev.Version, id: ev.ID, value: ev.Value, null: ev.Null}
		if cur, ok := winner[ev.ObjectID]; !ok || lessEntry(cur, cand) {
			winner[ev.ObjectID] = cand
		}
	}
	set := make(map[string]struct{})
	for obj, entry := range winner {
		if !entry.null && entry.value == value {
			set[obj] = struct{}{}
		}
	}
	return sortedKeys(set)
}

// DistinctValues 返回当前依据字段下所有非空最终取值（供对照遍历）。
func (m *NaiveModel) DistinctValues() []string {
	winner := make(map[string]lwwEntry)
	for _, ev := range m.events {
		if ev.Property != m.basis {
			continue
		}
		cand := lwwEntry{version: ev.Version, id: ev.ID, value: ev.Value, null: ev.Null}
		if cur, ok := winner[ev.ObjectID]; !ok || lessEntry(cur, cand) {
			winner[ev.ObjectID] = cand
		}
	}
	set := make(map[string]struct{})
	for _, entry := range winner {
		if !entry.null {
			set[entry.value] = struct{}{}
		}
	}
	return sortedKeys(set)
}
