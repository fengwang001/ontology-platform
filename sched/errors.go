package sched

import "errors"

// 调度层错误，均可通过 errors.Is 与其他包错误区分。
var (
	// ErrNoSuchTenant 表示向未注册的租户提交任务。
	ErrNoSuchTenant = errors.New("sched: no such tenant")
	// ErrTenantBusy 表示租户仍有排队任务，拒绝移除。
	ErrTenantBusy = errors.New("sched: tenant has queued tasks")
)
