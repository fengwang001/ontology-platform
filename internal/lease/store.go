package lease

// Write 受 fencing 保护的资源写入。
//
// 只接受 "当前有效租约" 的 token：
//   - token 大于已发出的最大 token：ErrUnknownToken（从未签发）。
//   - token 曾签发但不是当前租约纪元的 token：ErrStaleToken
//     （被抢占、已过期后被他人抢占、或已释放）。
//   - token 就是当前纪元的 token 但租约已到期：ErrLeaseExpired。
//     设计决策（题面第四节）：到期那一刻（now == expiresAt）
//     写入同样被拒绝，与半开区间有效期一致。
//
// 所有拒绝路径都在修改 m.data 之前返回，保证不变量 4：
// 被拒绝的 Write 对资源内容零副作用。
func (m *Manager) Write(token uint64, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if token > m.lastToken {
		return ErrUnknownToken
	}
	if m.current == nil || m.current.token != token {
		return ErrStaleToken
	}
	if !m.current.valid(m.now()) {
		return ErrLeaseExpired
	}
	m.data[key] = val
	return nil
}

// Read 读当前已提交的资源内容。读不需要持有租约。
func (m *Manager) Read(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val, ok := m.data[key]
	return val, ok
}
