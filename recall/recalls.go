package recall

import "sort"

// recallEntry 为一条召回登记。解除仅把 active 置为 false 并从按药品的
// 未解除覆盖索引（byDrug）中摘除；byID 保留记录以供状态与历史查询。
type recallEntry struct {
	id      string
	drugID  string
	lotLow  string
	lotHigh string
	level   int
	issueAt int64
	active  bool
}

// covers 判断批号是否落入闭区间（字典序，两端含）。
func (r *recallEntry) covers(batchID string) bool {
	return r.lotLow <= batchID && batchID <= r.lotHigh
}

func (r *recallEntry) view() RecallView {
	return RecallView{
		ID: r.id, DrugID: r.drugID, LotLow: r.lotLow, LotHigh: r.lotHigh,
		Level: r.level, IssueAt: r.issueAt, Active: r.active,
	}
}

// recallModule 为按药品隔离的召回登记表。
// byDrug 只保存未解除召回，因此有效等级判定的开销只与
// "覆盖该药品的未解除召回数" 相关，与已解除召回总数、其他药品召回总数均无关。
type recallModule struct {
	byID   map[string]*recallEntry
	byDrug map[string][]*recallEntry
}

func newRecalls() *recallModule {
	return &recallModule{
		byID:   map[string]*recallEntry{},
		byDrug: map[string][]*recallEntry{},
	}
}

func (m *recallModule) hasID(id string) bool {
	_, ok := m.byID[id]
	return ok
}

func (m *recallModule) get(id string) (*recallEntry, bool) {
	r, ok := m.byID[id]
	return r, ok
}

func (m *recallModule) register(e *recallEntry) {
	e.active = true
	m.byID[e.id] = e
	m.byDrug[e.drugID] = append(m.byDrug[e.drugID], e)
}

// release 解除一条召回：只撤销该条登记。返回 false 表示编号不存在或已解除。
func (m *recallModule) release(id string) bool {
	e, ok := m.byID[id]
	if !ok || !e.active {
		return false
	}
	e.active = false
	arr := m.byDrug[e.drugID]
	for i, r := range arr {
		if r == e {
			m.byDrug[e.drugID] = append(arr[:i], arr[i+1:]...)
			break
		}
	}
	return true
}

// effective 计算某药品某批号的有效等级与并列最严的未解除召回集合。
// level 为 0 表示无覆盖。只遍历该药品的未解除召回，故：
//   - 与已解除召回总数无关；
//   - 与其他药品的召回总数无关。
func (m *recallModule) effective(drugID, batchID string) (level int, ids []string) {
	for _, r := range m.byDrug[drugID] {
		if !r.covers(batchID) {
			continue
		}
		switch {
		case level == 0 || r.level < level:
			level = r.level
			ids = []string{r.id}
		case r.level == level:
			ids = append(ids, r.id)
		}
	}
	sort.Strings(ids)
	return level, ids
}

// activeDrugRecalls 返回某药品全部未解除召回（注册先后有序）。
func (m *recallModule) activeDrugRecalls(drugID string) []*recallEntry {
	return m.byDrug[drugID]
}
