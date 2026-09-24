// Package task 定义调度器处理的最小工作单元。
package task

// Task 是一个待执行任务：属于某租户，携带代价与全局提交序号。
type Task struct {
	Tenant string  // 所属租户 ID
	Cost   float64 // 执行代价；<=0 合法，vt 记账时按 MinCost 下界处理
	Seq    uint64  // 提交序号，由提交方分配，用于去重与确定性断言
}
