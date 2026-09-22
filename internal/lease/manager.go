package lease

import "sync"

// Manager 是带 fencing token 的租约管理器。
// 所有方法均可被多个 goroutine 并发调用。
type Manager struct {
	mu  sync.Mutex
	now func() int64

	// token 是最近一次发出的 fencing token，严格单调递增，永不回退。
	// 即使租约被释放，计数器也不复位（不变量 2）。
	token uint64

	// holder/expiresAt/held 描述"当前记录的租约"。
	// 租约有效当且仅当 held && now() < expiresAt（左闭右开区间）。
	// 过期后记录仍保留，用于把 ErrLeaseExpired 与 ErrNotHolder 区分开；
	// 只有 Release 成功或他人 Acquire 才会清除/覆盖该记录。
	held      bool
	holder    string
	expiresAt int64

	// store 是受 fencing 保护的资源内容。
	store map[string]string
}

// New 创建一个租约管理器。now 返回当前逻辑时刻（毫秒），
// 由外部注入，实现内部不调用 time.Now()。
func New(now func() int64) *Manager {
	return &Manager{
		now:   now,
		store: make(map[string]string),
	}
}

// validLocked 报告当前记录的租约是否在逻辑时刻上仍然有效。
// 调用时必须持有 m.mu。
func (m *Manager) validLocked(now int64) bool {
	return m.held && now < m.expiresAt
}

// Acquire 取得租约；成功时返回严格递增的 fencing token。
//
// 设计决策：同一 holder 在自己租约仍有效时再次 Acquire，
// 视为"重新获取"——签发一个全新 token 并刷新到期时刻，而不是报错。
// 理由：不变量 1（互斥）不受影响，因为持有者没变；不变量 2 允许
// 任意次成功 Acquire 只要 token 严格递增。客户端在丢失旧 token
// （如重启后状态未持久化）时可以用同一身份安全地重新获取，
// 旧 token 立即失效，fencing 语义不被破坏。
func (m *Manager) Acquire(holder string, ttlMillis int64) (uint64, error) {
	if holder == "" {
		return 0, ErrInvalidHolder
	}
	if ttlMillis <= 0 {
		return 0, ErrInvalidTTL
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.held && m.holder != holder && now < m.expiresAt {
		return 0, ErrLeaseHeld
	}

	m.token++
	m.held = true
	m.holder = holder
	m.expiresAt = now + ttlMillis
	return m.token, nil
}

// Renew 续约：延长自己的租约。holder 与 token 都必须匹配当前租约。
//
// 错误类别（均为哨兵错误，可用 errors.Is 判定）：
//   - holder 不是当前记录的持有者（被抢占/从未持有/已释放）：ErrNotHolder
//   - holder 匹配但 token 不符：ErrTokenMismatch
//   - holder 与 token 都匹配但租约已过期：ErrLeaseExpired
func (m *Manager) Renew(holder string, token uint64, ttlMillis int64) error {
	if holder == "" {
		return ErrInvalidHolder
	}
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.held || m.holder != holder {
		return ErrNotHolder
	}
	if token != m.token {
		return ErrTokenMismatch
	}
	now := m.now()
	if now >= m.expiresAt {
		return ErrLeaseExpired
	}

	m.expiresAt = now + ttlMillis
	return nil
}

// Release 释放租约。
//
// 设计决策：holder 与 token 都匹配当前记录时，即使租约已经过期，
// Release 也算成功（幂等清理）。理由：释放的期望终态是
// "该 holder 不再持有租约"，而过期的租约本来就已失效，
// 让清理路径幂等可以简化客户端的 defer Release 写法，
// 且不违反任何一条不变量。但 holder 不匹配（已被他人抢占）
// 仍返回 ErrNotHolder，token 不符仍返回 ErrTokenMismatch，
// 防止误清他人的租约记录。
func (m *Manager) Release(holder string, token uint64) error {
	if holder == "" {
		return ErrInvalidHolder
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.held || m.holder != holder {
		return ErrNotHolder
	}
	if token != m.token {
		return ErrTokenMismatch
	}

	// 清除租约记录，但 token 计数器不复位（不变量 2）。
	m.held = false
	m.holder = ""
	m.expiresAt = 0
	return nil
}
