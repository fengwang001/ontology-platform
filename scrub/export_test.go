package scrub

// replicasOf 返回某块当前按节点排序的副本副本（测试观测用）。
func (s *Store) replicasOf(blockID int) ([]Replica, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.blocks[blockID]
	if b == nil {
		return nil, false
	}
	return b.orderedReplicas(), true
}

func (s *Store) clockNow() Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

func (s *Store) dueComparisons() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.due.comparisons()
}

// ReplicasForTest 仅供包外测试（scrub_test）观测块状态。
func (s *Store) ReplicasForTest(blockID int) ([]Replica, bool) {
	return s.replicasOf(blockID)
}

// ClockForTest 仅供包外测试读取逻辑时钟。
func (s *Store) ClockForTest() Time { return s.clockNow() }
