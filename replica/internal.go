package replica

import "time"

// syncedList 按注册顺序返回同步副本集快照，保证输出可复现。
// 调用方必须持有 s.mu。
func (s *SyncSet) syncedList() []ReplicaID {
	out := make([]ReplicaID, 0, len(s.synced))
	for _, id := range s.order {
		if s.synced[id] {
			out = append(out, id)
		}
	}
	return out
}

// advanceHWM 将高水位推进到同步副本集内全部副本（含领导者）
// 日志结束位点的最小值。高水位只进不退。
// 调用方必须持有 s.mu。
func (s *SyncSet) advanceHWM(reason string) EndOffset {
	if len(s.synced) == 0 {
		// 不变量：领导者始终在同步副本集中。
		panic("replica: synced set must always contain the leader")
	}
	min := s.progs[s.leader].end
	for id := range s.synced {
		if e := s.progs[id].end; e < min {
			min = e
		}
	}
	if min < s.hwm {
		// 不变量：高水位不超过同步副本集内任一进度，故不可能下降。
		panic("replica: high watermark attempted to go backwards")
	}
	if min > s.hwm {
		s.logger.Printf("hwm %s: %d -> %d synced=%v", reason, s.hwm, min, s.syncedList())
		s.hwm = min
	}
	return s.hwm
}

// reject 记录无锁路径上的拒绝（参数预检失败时使用）。
func (s *SyncSet) reject(op string, format string, args ...any) {
	s.logger.Printf("reject op=%s reason="+format, append([]any{op}, args...)...)
}

// rejectLocked 记录持锁路径上的拒绝。调用方必须持有 s.mu。
func (s *SyncSet) rejectLocked(op string, format string, args ...any) {
	s.reject(op, format, args...)
}

// checkClock 在任何变更前校验逻辑时钟单调，回退则整体拒绝。
// 调用方必须持有 s.mu。
func (s *SyncSet) checkClock(op string, now time.Time) error {
	if now.Before(s.lastClock) {
		s.rejectLocked(op, "clock rollback now=%s last=%s",
			now.Format(time.RFC3339Nano), s.lastClock.Format(time.RFC3339Nano))
		return ErrClockRollback
	}
	return nil
}
