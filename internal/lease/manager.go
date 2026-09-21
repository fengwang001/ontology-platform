// Package lease 实现带 fencing token 的租约管理器。
//
// 时间完全由外部注入（now 回调），包内绝不调用 time.Now()，
// 因此租约的过期行为可以在测试中用逻辑时钟精确驱动。
//
// 核心不变量：
//  1. 互斥：任意时刻至多一个持有者持有有效租约。
//  2. token 严格单调递增，永不重复、永不回退。
//  3. fencing：Write 只接受当前有效租约的 token。
//  4. 被拒绝的 Write 无任何副作用。
//  5. 租约到期即失效，与持有者是否察觉无关。
//
// 过期边界的推导结论（题面第四节）：租约有效期是半开区间
// [获得时刻, 获得时刻+ttl)。now 恰好等于到期时刻时租约已失效，
// 此刻的 Renew/Write 一律拒绝。理由：ttl 是一段时长，半开区间
// 保证租约存活的毫秒数恰好等于 ttl；若把边界算作有效，租约会
// 多活一毫秒，且 "过期即失效" 的判定会出现歧义。
package lease

import "sync"

// lease 是当前租约纪元（epoch）的记录。被 Release 后置空；
// 自然过期但无人抢占时记录保留，以便对旧持有者给出
// ErrLeaseExpired 而非 ErrNotHolder 的精确错误。
type lease struct {
	holder    string
	token     uint64
	expiresAt int64 // 排他上界：now < expiresAt 时租约有效
}

// Manager 管理单个租约槽位和受其保护的键值资源。
// 所有方法都可被多个 goroutine 并发调用。
type Manager struct {
	mu  sync.Mutex
	now func() int64

	lastToken uint64 // 已发出的最大 token；首个 token 为 1
	current   *lease // nil 表示当前无租约纪元（从未签发或已释放）

	data map[string]string
}

// New 创建一个租约管理器。now 返回当前逻辑时刻（毫秒），
// 由调用方注入；nil 会 panic。
func New(now func() int64) *Manager {
	if now == nil {
		panic("lease: now func must not be nil")
	}
	return &Manager{
		now:  now,
		data: make(map[string]string),
	}
}

// valid 报告当前租约纪元在 now 时刻是否有效。
// 半开区间：now == expiresAt 时已失效。
func (l *lease) valid(now int64) bool {
	return now < l.expiresAt
}
