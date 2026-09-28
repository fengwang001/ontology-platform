package multilevel

// Engine 并发安全地维护明细组、第一维小计、总计三层计数与求和，
// 以及自创建以来的全部变更日志。
type Engine struct {
	maxDetailGroups int
}

// New 创建引擎；maxDetailGroups <= 0 表示不限制明细组数。
func New(maxDetailGroups int) *Engine {
	return &Engine{maxDetailGroups: maxDetailGroups}
}

// Insert 提交一条插入增量；失败时整体拒绝，不留任何痕迹。
func (e *Engine) Insert(row Row) ([]Change, error) {
	return nil, nil
}

// Delete 提交一条删除增量，撤回一条当前确实存在的行；
// 失败时整体拒绝，不留任何痕迹。
func (e *Engine) Delete(row Row) ([]Change, error) {
	return nil, nil
}

// Log 返回截至调用时刻全部变更日志的有序快照。
func (e *Engine) Log() []Change {
	return nil
}

// View 返回三层全部现存组（计数大于零）的快照。
func (e *Engine) View() []GroupView {
	return nil
}

// SelfCheck 校验三层不变量；不一致时返回描述错误。
func (e *Engine) SelfCheck() error {
	return nil
}
