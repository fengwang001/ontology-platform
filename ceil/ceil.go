// Package ceil 记录每个资源的使用者集合，并增量计算其优先级天花板：
// ceiling(res) = 所有声明使用 res 的任务中最高的基础优先级。
// 本包不依赖项目内其他包。
package ceil

// Table 把资源名映射到「声明使用它的任务 -> 该任务基础优先级」，
// 并为每个资源缓存当前天花板。
type Table struct {
	users   map[string]map[string]int
	ceiling map[string]int
}

// NewTable 创建空的天花板表。
func NewTable() *Table {
	return &Table{
		users:   map[string]map[string]int{},
		ceiling: map[string]int{},
	}
}

// AddResource 登记一个尚无人使用的资源，重复登记幂等。
func (t *Table) AddResource(name string) {
	if _, ok := t.users[name]; !ok {
		t.users[name] = map[string]int{}
	}
}

// HasResource 报告 name 是否已通过 AddResource 登记。
func (t *Table) HasResource(name string) bool {
	_, ok := t.users[name]
	return ok
}

// Declare 记录 task 以基础优先级 prio 声明使用 res。
// 同一对 (task, res) 重复声明幂等；天花板只做增量 max，不扫描使用者。
func (t *Table) Declare(res, task string, prio int) {
	u := t.users[res]
	if u == nil {
		u = map[string]int{}
		t.users[res] = u
	}
	if u[task] == prio {
		return
	}
	u[task] = prio
	if prio > t.ceiling[res] {
		t.ceiling[res] = prio
	}
}

// Ceiling 返回 res 的当前天花板；资源不存在或尚无使用者时为 0。
func (t *Table) Ceiling(res string) int {
	return t.ceiling[res]
}
