package allocator

import "context"

// Watermark 是持久化的「已预留到」高水位：
// 版本 Version 中 [0, NextSeq) 的序号都已预留；
// NextSeq 等于 N 表示该版本序号已全部预留完。
type Watermark struct {
	Version int
	NextSeq int
}

// Store 是租户序号高水位的持久化层抽象。
//
// 语义约定：
//   - 返回 ReserveOK：新水位确定已落盘，崩溃后仍可读出；
//   - 返回 ReserveFailed：确定未落盘，持久化保持旧水位不变；
//   - 返回 ReserveUnknown：结果未知（可能已落盘），调用方按已落盘处理。
type Store interface {
	// RegisterTenant 创建租户并写入初始水位 (1, 0)。
	// 租户已存在时返回 ErrTenantExists。
	RegisterTenant(ctx context.Context, tenant string, n, b, v int, wm Watermark) error

	// LoadTenant 读取租户配置与水位；租户不存在时返回 ErrTenantNotFound。
	LoadTenant(ctx context.Context, tenant string) (n, b, v int, wm Watermark, err error)

	// ListTenants 返回全部已注册租户，用于启动时恢复。
	ListTenants(ctx context.Context) ([]string, error)

	// CompareAndSwapReserve 仅当持久化当前水位等于 old 时把水位推进到 next。
	// 当 status 为 ReserveFailed 时保证持久化未改变；
	// 当 status 为 ReserveUnknown 时持久化可能已改变为 next。
	CompareAndSwapReserve(ctx context.Context, tenant string, old, next Watermark) (status ReserveStatus, err error)
}
