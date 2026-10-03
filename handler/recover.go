package handler

import (
	"ontology/dedupe"
	"ontology/history"
)

// Recover 丢弃实例内存，只凭历史重建状态与去重表。
// 对任意历史前缀成立：s 为按序前 m 个 U（m = A 事件数）的 delta 之和，
// 其余 U 为待应用队列；C 之后仍未被 A 覆盖的 U 结果为 Aborted。
func (h *Handler) Recover(instance []byte) error {
	if len(instance) == 0 {
		return ErrInvalidParam
	}
	h.mu.RLock()
	st := h.m[string(instance)]
	h.mu.RUnlock()
	if st == nil {
		return ErrNotFound
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	events := st.log.Events()
	updates := make([]pending, 0, len(events)) // 按序收集全部 U
	applied := 0                               // A 事件个数
	closedAt := 0                              // C 的事件序号；0 表示尚未关闭
	for _, e := range events {
		switch e.Type {
		case history.TypeUpdate:
			updates = append(updates, pending{
				uid:   append([]byte(nil), e.UID...),
				delta: e.Delta,
				seq:   e.Index,
			})
		case history.TypeApplied:
			applied++
		case history.TypeClosed:
			closedAt = int(e.Index)
		}
	}
	if applied > len(updates) {
		applied = len(updates) // 防御任意前缀
	}

	tab := dedupe.New(h.k)
	var s, p int64
	for i, u := range updates {
		if i < applied { // 前 m 个 U 已严格 FIFO 应用
			s += u.delta
			p = s
			tab.Put(u.uid, Result{Kind: Completed, Val: s})
			continue
		}
		p += u.delta
		if closedAt > 0 { // 关闭时仍未被 A 覆盖的 U 一律 Aborted
			tab.Put(u.uid, Result{Kind: Aborted})
		} else {
			tab.Put(u.uid, Result{Kind: Accepted, Seq: u.seq})
		}
	}

	st.dedup = tab
	st.s = s
	st.p = p
	st.closed = closedAt > 0
	if closedAt > 0 {
		st.queue = nil
	} else {
		st.queue = append([]pending(nil), updates[applied:]...)
	}
	return nil
}
