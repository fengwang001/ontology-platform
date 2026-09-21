package mux

import "time"

// waiter 表示一个在等的请求。ch 容量为 1，保证派发方永不阻塞。
type waiter struct {
	ch       chan []byte
	done     chan struct{} // 结束时关闭（派发、超时或关闭）
	deadline time.Time     // 零值表示永不超时
	payload  []byte        // 派发成功时的结果副本
	err      error         // 失败原因（ErrTimedOut / ErrClosed）
}

// register 登记一个等待者；deadline 为零值表示不参与 Tick 淘汰。
func (m *Mux) register(id string, deadline time.Time) (*waiter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if _, ok := m.pending[id]; ok {
		return nil, ErrDuplicateID
	}
	w := &waiter{
		ch:       make(chan []byte, 1),
		done:     make(chan struct{}),
		deadline: deadline,
	}
	m.pending[id] = w
	m.known[id] = true
	return w, nil
}

// resolveLocked 将等待者从 pending 移除并结束它。调用方须持有 m.mu。
// 每个等待者只会被 resolve 一次（先从 map 删除再结束）。
func (m *Mux) resolveLocked(id string, w *waiter, payload []byte, err error) {
	delete(m.pending, id)
	w.payload = payload
	w.err = err
	if payload != nil {
		w.ch <- payload
	}
	close(w.done)
}
