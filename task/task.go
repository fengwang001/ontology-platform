// Package task 定义调度任务：所属租户、代价与全局提交序号。
package task

import "time"

// Task 是不可变的调度单元。
//
// Tenant 为租户 ID；Cost 为执行代价（允许 0，见 DESIGN.md 第 4 节）；
// Seq 为提交序号，调用方保证全局唯一，用于不丢不重校验；
// At 为任务被调度执行时刻，由调度器用注入时钟写入。
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64
	At     time.Time
}

// New 构造一个任务。At 留空，由调度器在执行时填写。
func New(tenant string, cost float64, seq int64) Task {
	return Task{Tenant: tenant, Cost: cost, Seq: seq}

}
