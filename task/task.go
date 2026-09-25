// Package task 描述被调度的工作单元：归属租户、代价、提交序号。
package task

// Task 是一次提交。Seq 为全局提交序号（按入队锁串行化顺序分配），
// 在同租户同虚拟时间的并列场景下作为最终稳定次序的依据。
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64
}

// New 构造任务。调用方负责代价合法性与序号分配。
func New(tenant string, cost float64, seq int64) Task {
	return Task{Tenant: tenant, Cost: cost, Seq: seq}
}
