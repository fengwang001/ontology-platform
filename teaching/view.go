package teaching

import "sort"

// Touch 推进逻辑时钟并触发惰性超时释放；无其他副作用。
func (s *Service) Touch(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	s.now = now
	s.sweepExpiredLocked()
	return nil
}

// Now 返回当前逻辑时钟。
func (s *Service) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// ActiveLoad 返回教师当前活跃（待确认+已生效）折算工作量累计。
func (s *Service) ActiveLoad(teacherID string, now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if teacherID == "" {
		return 0, errf(ErrInvalidParameter, "empty teacher id")
	}
	if now < s.now {
		return 0, errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	s.now = now
	s.sweepExpiredLocked()
	if _, ok := s.teachers[teacherID]; !ok {
		return 0, errf(ErrNotFound, "teacher not found")
	}
	return s.load[teacherID], nil
}

// ListAssignments 返回教师所有指派的确定性顺序快照（按任务 ID 排序）。
func (s *Service) ListAssignments(teacherID string, now int64) ([]AssignmentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if teacherID == "" {
		return nil, errf(ErrInvalidParameter, "empty teacher id")
	}
	if now < s.now {
		return nil, errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	s.now = now
	s.sweepExpiredLocked()
	if _, ok := s.teachers[teacherID]; !ok {
		return nil, errf(ErrNotFound, "teacher not found")
	}
	var views []AssignmentView
	for _, a := range s.assignments {
		if a.teacherID != teacherID {
			continue
		}
		views = append(views, AssignmentView{
			TeacherID: a.teacherID,
			TaskID:    a.taskID,
			Hours:     a.hours,
			Status:    a.status,
			Deadline:  a.deadline,
			WeekStart: a.weekStart,
			WeekEnd:   a.weekEnd,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].TaskID < views[j].TaskID })
	return views, nil
}

// Occupied 返回教师某周某节次当前占用该格的任务 ID（空串表示空闲）。
// 复杂度 O(1) map 查询，供可验证地证明冲突判定不随历史任务数增长。
func (s *Service) Occupied(teacherID string, week, period int, now int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if teacherID == "" || week <= 0 || period <= 0 {
		return "", errf(ErrInvalidParameter, "invalid query")
	}
	if now < s.now {
		return "", errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	s.now = now
	s.sweepExpiredLocked()
	if _, ok := s.teachers[teacherID]; !ok {
		return "", errf(ErrNotFound, "teacher not found")
	}
	return s.occ[teacherID][occKey{week, period}], nil
}

// TaskAllocated 返回任务当前活跃学时合计（守恒断言用）。
func (s *Service) TaskAllocated(taskID string, now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskID == "" {
		return 0, errf(ErrInvalidParameter, "empty task id")
	}
	if now < s.now {
		return 0, errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	s.now = now
	s.sweepExpiredLocked()
	if _, ok := s.tasks[taskID]; !ok {
		return 0, errf(ErrNotFound, "task not found")
	}
	return s.allocated[taskID], nil
}
