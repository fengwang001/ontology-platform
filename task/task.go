// Package task 定义被调度的任务：所属租户、代价与全局提交序号。
package task

import (
	"errors"
	"math"
)

// 代价类错误，可用 errors.Is 判定。
var (
	// ErrBadCost 表示代价为 NaN/Inf 或超出允许区间 [0, 1e100]。
	ErrBadCost = errors.New("task: invalid cost")
)

// MaxCost 是允许的单个任务代价上界。
const MaxCost = 1e100

// Task 是调度的最小单位。Seq 由调度器在入队串行化点分配，
// 因而并发提交时 Seq 即“获取入队锁的顺序”。
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64
}

// ValidCost 判定代价合法：有限、非负、不超过 MaxCost。
func ValidCost(c float64) bool {
	return !math.IsNaN(c) && !math.IsInf(c, 0) && c >= 0 && c <= MaxCost
}

// New 构造任务并校验代价。
func New(tenant string, cost float64, seq int64) (Task, error) {
	if !ValidCost(cost) {
		return Task{}, ErrBadCost
	}
	return Task{Tenant: tenant, Cost: cost, Seq: seq}, nil
}
