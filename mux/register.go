package mux

import "time"

// Register 登记一个等待者，返回用于取结果的 channel。
// 通过 Register 登记的等待者不参与 Tick 超时淘汰，
// 只会被 Deliver 满足或被 Close 中断。
func (m *Mux) Register(id string) (<-chan []byte, error) {
	w, err := m.register(id, time.Time{})
	if err != nil {
		return nil, err
	}
	return w.ch, nil
}

// Wait 登记并阻塞等待 id 的响应。
// 超时由注入时钟判定：Tick 推进到 deadline 时返回 ErrTimedOut；
// 匹配器关闭时返回 ErrClosed。
func (m *Mux) Wait(id string, deadline time.Time) ([]byte, error) {
	w, err := m.register(id, deadline)
	if err != nil {
		return nil, err
	}
	<-w.done
	if w.err != nil {
		return nil, w.err
	}
	return w.payload, nil
}
