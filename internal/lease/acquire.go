package lease

// Acquire 取得租约，成功时返回严格递增的 fencing token（从 1 开始）。
//
// 仅当当前没有有效租约（从未签发、已过期、已释放）时成功。
// 当前持有者自己重复 Acquire 返回 ErrAlreadyHeld（见 errors.go
// 中该决策的理由）；他人持有有效租约时返回 ErrLeaseHeld。
func (m *Manager) Acquire(holder string, ttlMillis int64) (uint64, error) {
	if holder == "" {
		return 0, ErrEmptyHolder
	}
	if ttlMillis <= 0 {
		return 0, ErrInvalidTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.current != nil && m.current.valid(now) {
		if m.current.holder == holder {
			return 0, ErrAlreadyHeld
		}
		return 0, ErrLeaseHeld
	}
	// 在锁内先递增再签发，保证并发抢租约时只有一个成功者，
	// 且 token 严格单调、不重不漏。
	m.lastToken++
	m.current = &lease{
		holder:    holder,
		token:     m.lastToken,
		expiresAt: now + ttlMillis,
	}
	return m.lastToken, nil
}

// Renew 续约：把当前持有者的租约延长为 now+ttlMillis。
//
// 设计决策（题面第四节）：
//   - 续约从当前时刻起算（now+ttl），而非在旧到期时刻上累加，
//     避免 "提前续约反而缩短/膨胀有效期" 的歧义。
//   - 在到期那一刻（now == expiresAt）续约返回 ErrLeaseExpired，
//     与半开区间的过期判定一致。
//   - 被抢占者（或 token 不匹配者）续约返回 ErrNotHolder，
//     与 ErrLeaseExpired 不是同一类错误。
func (m *Manager) Renew(holder string, token uint64, ttlMillis int64) error {
	if holder == "" {
		return ErrEmptyHolder
	}
	if ttlMillis <= 0 {
		return ErrInvalidTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.current == nil || m.current.holder != holder || m.current.token != token {
		return ErrNotHolder
	}
	if !m.current.valid(now) {
		return ErrLeaseExpired
	}
	m.current.expiresAt = now + ttlMillis
	return nil
}

// Release 释放当前租约。
//
// 设计决策（题面第四节）：释放一个已过期或已被抢占的租约算失败。
// 已过期返回 ErrLeaseExpired，已被抢占/已释放/token 不匹配返回
// ErrNotHolder。理由：此时租约已无效，"释放" 在状态上是空操作，
// 若返回成功会掩盖调用方 "我以为我还持有" 的认知错误；报错更
// 诚实，且不违反任何不变量（状态本来就不会变）。
func (m *Manager) Release(holder string, token uint64) error {
	if holder == "" {
		return ErrEmptyHolder
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.current == nil || m.current.holder != holder || m.current.token != token {
		return ErrNotHolder
	}
	if !m.current.valid(now) {
		return ErrLeaseExpired
	}
	m.current = nil
	return nil
}
