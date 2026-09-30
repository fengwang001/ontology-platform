package keystream

import "context"

// TenantRecord 是持久层中一个租户的高水位记录。
type TenantRecord struct {
	// N 每个版本可用序号数（每版本序号为 0..N-1）。
	N int64
	// B 一批预留的序号数（不跨版本边界）。
	B int64
	// V 最大版本数（版本 1..V）。
	V int64
	// Reserved 已预留高水位：跨版本累计已预留的序号槽位数。
	// 版本号 = Reserved/N + 1，版本内序号 = Reserved%N。
	Reserved int64
}

// Store 是高水位持久化层。
//
// SaveReserved 把租户的高水位原子推进到 reserved（reserved 只会单调
// 增大）。返回 nil 表示确认落盘；返回 *PersistError 表示失败，其
// Unknown 字段区分“明确失败”与“结果未知”。
type Store interface {
	// CreateTenant 创建租户记录，初始高水位为 0。已存在时返回
	// ErrStoreConflict（包装为 PersistError 亦可）。
	CreateTenant(ctx context.Context, tenant string, rec TenantRecord) error
	// GetTenant 读取租户记录；不存在时 ok 为 false。
	GetTenant(ctx context.Context, tenant string) (rec TenantRecord, ok bool, err error)
	// SaveReserved 把高水位推进到 reserved。
	SaveReserved(ctx context.Context, tenant string, reserved int64) error
	// ListTenants 列出全部已注册租户，供崩溃恢复使用。
	ListTenants(ctx context.Context) ([]string, error)
}
