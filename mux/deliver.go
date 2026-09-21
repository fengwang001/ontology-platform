package mux

// Deliver 派发一个响应。payload 会被拷贝，调用方之后改写原切片不影响已派发内容。
// 无人认领时：该 id 曾注册过计入 Late，从未注册过计入 Orphans，payload 丢弃。
func (m *Mux) Deliver(id string, payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.pending[id]
	if !ok {
		if m.known[id] {
			m.late++
		} else {
			m.orphans++
		}
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	m.delivered++
	m.resolveLocked(id, w, cp, nil)
}
