// Package task 定义调度任务与跨包共用的哨兵错误。
package task

import (
	"errors"
	"math"
)

// 四类可用 errors.Is 区分的错误。
var (
	// ErrQueueFull 租户队列已满（背压）。
	ErrQueueFull = errors.New("task: tenant queue full")
	// ErrInvalidWeight 权重为 0、负、NaN 或 Inf。
	ErrInvalidWeight = errors.New("task: invalid weight")
	// ErrInvalidCost 代价为负、NaN 或 Inf。
	ErrInvalidCost = errors.New("task: invalid cost")
	// ErrTenantBusy 租户仍有排队任务，不能移除。
	ErrTenantBusy = errors.New("task: tenant has queued tasks")
)

// Task 是一个待调度任务。
type Task struct {
	// Tenant 为租户 ID（并列时按其字典序打破）。
	Tenant string
	// Cost 为任务代价，允许为 0。
	Cost float64
	// Seq 为租户内单调提交序号，用于去重与确定性。
	Seq int64
}

// ValidateWeight 校验权重：必须为有限正数。
func ValidateWeight(w float64) error {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return ErrInvalidWeight
	}
	return nil
}

// ValidateCost 校验代价：必须为有限非负数。
func ValidateCost(c float64) error {
	if math.IsNaN(c) || math.IsInf(c, -1) || math.IsInf(c, 1) || c < 0 {
		return ErrInvalidCost
	}
	return nil
}

// New 构造并校验一个任务。
func New(tenant string, cost float64, seq int64) (Task, error) {
	if tenant == "" {
		return Task{}, ErrInvalidCost
	}
	if err := ValidateCost(cost); err != nil {
		return Task{}, err
	}
	return Task{Tenant: tenant, Cost: cost, Seq: seq}, nil
}
