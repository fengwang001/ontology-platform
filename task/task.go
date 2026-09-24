// Package task 定义调度器处理的最小工作单元。
package task

// Task 是一个租户提交的一次性工作单元。
type Task struct {
	// Tenant 为提交该任务的租户 ID。
	Tenant string
	// Cost 为任务的执行代价，必须非负；0 合法，
	// 调度器推进虚拟时间时会施加最小代价下界。
	Cost float64
	// Seq 为提交方分配的提交序号，用于并发测试中去重与计数。
	Seq uint64
}
