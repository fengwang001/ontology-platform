package ontology

import "sync"

// CostMeter 以可验证的方式度量一次操作触碰的数据规模。
//
//   - InstanceRecordReads：读取的源实例记录条数。聚合查询路径恒为 0，
//     即查询开销不随对象类型下全部实例总数增长；朴素重算路径则与实例
//     总数线性相关，二者形成可直接断言的对照。
//   - GroupCellsTouched：触碰的分组单元数。查询恒为 1；一次提交只触碰
//     「旧分组 + 新分组」共至多 2 个单元/视图，与该类型实例总数无关。
type CostMeter struct {
	InstanceRecordReads int
	GroupCellsTouched   int
}

// GroupValue 是一个聚合分组的当前取值。
type GroupValue struct {
	View    string
	Group   string
	Sum     float64
	Members int // 该分组当前存活成员规模（仅用于成本证明与可读性）
}

// groupCell 是 (视图, 分组键) 的增量汇总单元。
type groupCell struct {
	sum     float64
	members int
}

// aggregateIndex 是「聚合视图增量维护模块」：
// 对每个 (视图, 分组键) 维护增量汇总值与成员规模。视图不持久化成员列表
// 快照——只保存由提交事件推导出的汇总；任何时刻查询结果都等价于用当时
// 源实例最新可见版本重新计算（rebuild 提供同构的自校验手段）。
type aggregateIndex struct {
	mu sync.RWMutex
	// viewName -> groupKey -> cell
	cells map[string]map[string]*groupCell
}

func newAggregateIndex() *aggregateIndex {
	return &aggregateIndex{cells: map[string]map[string]*groupCell{}}
}

// delta 描述一次提交对单个视图的贡献变化：从旧分组扣除 old，向新分组计入 cur。
// 删除时 curGroup 为空；首次插入时 oldGroup 为空；分组不变时二者相同。
type delta struct {
	view     AggregateDef
	oldGroup string
	newGroup string
	oldVal   float64
	newVal   float64
	// hadOld 表示提交前该实例是存活成员（需要扣除旧贡献）。
	hadOld bool
	// hasNew 表示提交后该实例是存活成员（需要计入新贡献）。
	hasNew bool
}

// apply 在唯一提交锁内应用一次提交对全部相关视图的增量。同一视图的旧分组
// 扣除与新分组计入在本方法内连续完成、不可分割；查询只能拿到整把读锁
// 保护下的一致状态，因此分组迁移不会出现「同时在两组」或「两组都消失」的
// 中间态。多个视图的更新同处内核的同一临界区，故对一次提交整体可见。
func (a *aggregateIndex) apply(deltas []delta, meter *CostMeter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range deltas {
		groups := a.cells[d.view.Name]
		if groups == nil {
			groups = map[string]*groupCell{}
			a.cells[d.view.Name] = groups
		}
		if d.hadOld {
			cell := groups[d.oldGroup]
			cell.sum -= d.oldVal
			cell.members--
			if meter != nil {
				meter.GroupCellsTouched++
			}
			if cell.members == 0 {
				delete(groups, d.oldGroup)
			}
		}
		if d.hasNew {
			cell := groups[d.newGroup]
			if cell == nil {
				cell = &groupCell{}
				groups[d.newGroup] = cell
			}
			cell.sum += d.newVal
			cell.members++
			if meter != nil {
				meter.GroupCellsTouched++
			}
		}
	}
}

// get 只读取一个分组单元：O(1)，不触碰任何源实例记录。
func (a *aggregateIndex) get(view, group string, meter *CostMeter) GroupValue {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if meter != nil {
		meter.GroupCellsTouched++
	}
	out := GroupValue{View: view, Group: group}
	if groups, ok := a.cells[view]; ok {
		if cell, ok := groups[group]; ok {
			out.Sum = cell.sum
			out.Members = cell.members
		}
	}
	return out
}

// rebuild 用源实例的全量快照重算某一视图，声明并验证「视图是源实例纯粹
// 派生物」：正常路径上只靠 apply 增量维护，rebuild 的结果必须与其一致。
func (a *aggregateIndex) rebuild(view AggregateDef, records []Record) map[string]GroupValue {
	groups := map[string]*groupCell{}
	for _, rec := range records {
		if rec.Deleted {
			continue
		}
		gv, ok := rec.Attrs[view.GroupBy]
		if !ok {
			continue
		}
		vv, ok := rec.Attrs[view.ValueField]
		if !ok {
			continue
		}
		cell := groups[gv.Str]
		if cell == nil {
			cell = &groupCell{}
			groups[gv.Str] = cell
		}
		cell.sum += vv.Num
		cell.members++
	}
	out := make(map[string]GroupValue, len(groups))
	for g, cell := range groups {
		out[g] = GroupValue{View: view.Name, Group: g, Sum: cell.sum, Members: cell.members}
	}
	return out
}

// snapshotView 返回某视图当前全部分组单元的副本。调用方必须已持有内核读栅栏，
// 因此此处不再取索引锁，与 rebuild/读取处于同一观测时刻。
func (a *aggregateIndex) snapshotView(view string) []GroupValue {
	out := make([]GroupValue, 0, len(a.cells[view]))
	for g, cell := range a.cells[view] {
		out = append(out, GroupValue{View: view, Group: g, Sum: cell.sum, Members: cell.members})
	}
	return out
}
