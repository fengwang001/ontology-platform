package sched

import "ontology/group"

// StateOf 返回运行状态；不存在时第二个返回值为 false。
func (s *Scheduler) StateOf(id int) (group.State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[id]
	if r == nil {
		return 0, false
	}
	return r.State, true
}

// Queue 返回等待队列从队首到队尾的运行编号快照。
func (s *Scheduler) Queue() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pool.IDs()
}

// Busy 返回当前占用执行位的运行数（Running + Cancelling）。
func (s *Scheduler) Busy() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pool.Busy()
}

// lastTouched 返回最近一次公开操作触达的不同运行记录数。
// 非导出计数器的测试观察口：用于证明单次操作触达记录数与组/队列规模无关。
func (s *Scheduler) lastTouched() int { return len(s.touched) }

// lastPeakQueue 返回最近一次操作中（晋升之后、分配之前）观察到的队列长度峰值。
// 测试专用：证明 Pending 晋升不受 Q 约束、队列可暂时超过 Q。
func (s *Scheduler) lastPeakQueue() int { return s.peakQueue }

// snapshotStates 返回 id -> 状态 的快照（测试校验不变量用）。
func (s *Scheduler) snapshotStates() map[int]group.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]group.State, len(s.runs))
	for id, r := range s.runs {
		out[id] = r.State
	}
	return out
}
