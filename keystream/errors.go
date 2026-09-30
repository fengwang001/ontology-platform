package keystream

import "errors"

// 业务校验错误：与持久化错误可通过 errors.Is / errors.As 区分。
var (
	// ErrEmptyTenant 租户名为空。
	ErrEmptyTenant = errors.New("keystream: tenant is empty")
	// ErrTenantExists 注册时租户已存在。
	ErrTenantExists = errors.New("keystream: tenant already registered")
	// ErrInvalidConfig N、B、V 非正或 B 大于 N。
	ErrInvalidConfig = errors.New("keystream: invalid config: N, B, V must be positive and B must not exceed N")
	// ErrTenantNotFound 分配时租户未注册。
	ErrTenantNotFound = errors.New("keystream: tenant is not registered")
	// ErrKeyExhausted 版本数已用尽（所有版本的序号都已发放/预留完毕）。
	ErrKeyExhausted = errors.New("keystream: key versions exhausted")

	// ErrStoreConflict 持久层中的租户记录冲突（仅供 Store 实现使用）。
	ErrStoreConflict = errors.New("keystream: store conflict")
)

// PersistError 表示一次持久化操作失败。
//
// Unknown 为 false 表示明确失败：可以确定新高水位未落盘，内存与
// 持久层高水位均保持不变，本次分配失败，下一次分配重试同一批。
//
// Unknown 为 true 表示结果未知：高水位可能已经落盘。该批一律视为
// 已预留且整批作废（内存发放位置直接推进到该批之后），本次分配
// 失败，下一次分配从该批之后重新预留。
type PersistError struct {
	// Op 触发失败的操作名（如 "create_tenant"、"save_reserved"）。
	Op string
	// Unknown 为 true 表示结果未知；false 表示确定未落盘。
	Unknown bool
	// Err 底层错误。
	Err error
}

func (e *PersistError) Error() string {
	if e.Unknown {
		return "keystream: persist op " + e.Op + " result unknown: " + e.Err.Error()
	}
	return "keystream: persist op " + e.Op + " failed definitively: " + e.Err.Error()
}

func (e *PersistError) Unwrap() error { return e.Err }
