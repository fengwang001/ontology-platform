package groupagg

import "sync"

// rowRec 记录一行当前所属的分组与取值。
type rowRec struct {
	group string
	value int64
}

// Aggregator 是并发安全的增量分组聚合器。
//
// 所有写操作通过 Apply 以批为单位原子提交：批中任一操作非法则整批被拒绝，
// 行表、分组聚合与已产生日志均不受影响。读操作（Snapshot 等）可与写入并发进行，
// 始终看到某个已成功提交的批之后的一致视图。
type Aggregator struct {
	mu        sync.RWMutex
	rows      map[string]rowRec  // 行键 -> 当前行记录
	groups    map[string]GroupState // 分组键 -> 当前聚合（仅保留 Count > 0 的分组）
	maxGroups int                // 允许同时存在的不同分组数上限；<=0 表示不限制
}

// New 创建聚合器。maxGroups <= 0 表示不限制分组数量。
func New(maxGroups int) *Aggregator {
	return &Aggregator{
		rows:      make(map[string]rowRec),
		groups:    make(map[string]GroupState),
		maxGroups: maxGroups,
	}
}

// Apply 原子地应用一批行级操作，返回本批产生的确定性变更日志。
//
// 批中第一条非法操作会导致整批拒绝，返回 *BatchError，且不改变任何内部状态。
// 成功时返回的条目顺序固定：按操作顺序逐条处理；单条操作内先输出受影响分组的
// 撤回（Withdraw）、再输出写入（Upsert）；改分组键时旧分组的所有条目先于新分组。
func (a *Aggregator) Apply(ops []Op) ([]Entry, error) {
	// TODO: 下一文件实现
	return nil, nil
}

// Snapshot 返回当前所有 Count > 0 的分组聚合状态副本，可被并发安全读取。
// 返回的 map 由调用方独占，与批量重算结果一致。
func (a *Aggregator) Snapshot() map[string]GroupState {
	// TODO: 下一文件实现
	return nil
}

// RowCount 返回当前行数。
func (a *Aggregator) RowCount() int {
	// TODO: 下一文件实现
	return 0
}

// GroupCount 返回当前 Count > 0 的分组数。
func (a *Aggregator) GroupCount() int {
	// TODO: 下一文件实现
	return 0
}
