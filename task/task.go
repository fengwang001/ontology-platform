// Package task 定义被调度的任务单元：所属租户、代价、全局提交序号。
package task

// Task 是一次不可变的调度请求。Seq 由调度器在入队串行化点单调分配，
// 并发下"提交顺序"即 Seq 的顺序。
type Task struct {
	tenant string
	cost   float64
	seq    uint64
}

// New 构造任务（不校验；cost/weight 的校验在租户与 admit 层完成）。
func New(tenant string, cost float64, seq uint64) Task {
	return Task{tenant: tenant, cost: cost, seq: seq}
}

// Tenant 返回任务所属租户 ID。
func (t Task) Tenant() string { return t.tenant }

// Cost 返回任务声明的真实代价（可能为 0；统计使用此值）。
func (t Task) Cost() float64 { return t.cost }

// Seq 返回全局提交序号，用于去重与确定性校验。
func (t Task) Seq() uint64 { return t.seq }

// Zero 判断是否为零值任务。
func (t Task) Zero() bool {
	return t.tenant == "" && t.cost == 0 && t.seq == 0
}

// WithTenant 返回一份改了租户的副本（主要供测试构造）。
func (t Task) WithTenant(id string) Task {
	t.tenant = id
	return t
}
