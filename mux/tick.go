package mux

// Tick 按当前注入时钟淘汰已到达 deadline 的等待者，
// 使其 Wait 以 ErrTimedOut 结束。无 deadline 的等待者不受影响。
func (m *Mux) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, w := range m.pending {
		if w.deadline.IsZero() || now.Before(w.deadline) {
			continue
		}
		m.timedOut++
		m.resolveLocked(id, w, nil, ErrTimedOut)
	}
}

// Close 关闭匹配器：所有在等的请求以 ErrClosed 结束。
// 幂等；关闭后 Register 返回 ErrClosed，Deliver 只记账不派发。
func (m *Mux) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for id, w := range m.pending {
		m.interrupted++
		m.resolveLocked(id, w, nil, ErrClosed)
	}
}
