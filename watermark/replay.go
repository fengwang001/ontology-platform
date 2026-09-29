package watermark

// Replay 依据维护器按加锁总序记录的操作日志进行按序重放，
// 返回一个终态视图与原维护器完全一致的新维护器。
//
// 日志记录了每次摄入（含事件时间与处理时间戳）与每次心跳；
// 被整体拒绝的调用不产生日志。由于迟到判定只依赖事件时间，
// 重放结果与调用的并发交错无关，始终可复现。
func Replay(m *Maintainer) (*Maintainer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replayLocked()
}

// replayLocked 按日志顺序重建状态，调用方必须持有 m 的锁。
// 重放目标是独立的新实例，其内部加锁不会与 m 冲突。
func (m *Maintainer) replayLocked() (*Maintainer, error) {
	r, err := New(m.allowedLateness)
	if err != nil {
		return nil, err
	}
	for _, o := range m.log {
		switch o.kind {
		case opIngest:
			if _, err := r.Ingest(Event{ID: o.id, EventTime: o.eventTime}, o.processing); err != nil {
				return nil, err
			}
		case opHeartbeat:
			if err := r.Heartbeat(o.processing); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}
