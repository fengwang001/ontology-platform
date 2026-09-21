package lease

// Write 是受 fencing 保护的资源写入。
//
// 只接受"当前有效租约"的 token（不变量 3）：
//   - token 不等于当前记录的 token（被抢占者的旧 token、
//     从未发出过的值，一律同等对待）：ErrStaleToken
//   - token 匹配但租约已过期（含恰好到期的边界时刻）：ErrLeaseExpired
//
// 任何被拒绝的 Write 都不会触碰 store（不变量 4）：
// 所有校验都在写之前完成，失败路径没有任何副作用。
func (m *Manager) Write(token uint64, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.held || token != m.token {
		return ErrStaleToken
	}
	if now := m.now(); now >= m.expiresAt {
		return ErrLeaseExpired
	}

	m.store[key] = val
	return nil
}

// Read 读当前已提交的资源内容。第二个返回值表示 key 是否存在。
func (m *Manager) Read(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	val, ok := m.store[key]
	return val, ok
}
