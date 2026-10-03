package lessor

import "errors"

// 哨兵错误，调用方可用 errors.Is 判定拒绝原因。
var (
	// ErrInvalidParam 参数非法：id 小于 1、key 为空、ttl 越界。
	ErrInvalidParam = errors.New("lessor: invalid argument")
	// ErrInvalidTime 时间非法：now 越界或小于时钟水位 T。
	ErrInvalidTime = errors.New("lessor: invalid time")
	// ErrRole 角色错误：要求主而当前为从，或 Promote 时已是主、Demote 时已是从。
	ErrRole = errors.New("lessor: role error")
	// ErrLeaseExists 租约已存在。
	ErrLeaseExists = errors.New("lessor: lease already exists")
	// ErrLeaseNotFound 租约不存在。
	ErrLeaseNotFound = errors.New("lessor: lease not found")
	// ErrExpired 租约已过期（x <= now），仍留在积压中。
	ErrExpired = errors.New("lessor: lease expired")
	// ErrFull 租约挂靠键数已达 Kmax。
	ErrFull = errors.New("lessor: lease full")
)
