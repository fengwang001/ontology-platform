package teaching

// Replace 自 fromWeek（含）起把任务剩余部分从原教师交给新教师，立即生效。
// 学时按周均匀分布：base=hours/weeks，无法整除的零头全部归原教师。
// 失败则一切保持原状（所有校验先于任何状态变更）。
func (s *Service) Replace(fromTeacherID, taskID, toTeacherID string, fromWeek int, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fromTeacherID == "" || taskID == "" || toTeacherID == "" {
		return errf(ErrInvalidParameter, "empty id")
	}
	if fromTeacherID == toTeacherID {
		return errf(ErrInvalidParameter, "new teacher must differ from original")
	}
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	oldNow := s.now
	s.now = now
	swept := s.sweepExpiredLocked()

	task, tOK := s.tasks[taskID]
	_, fromOK := s.teachers[fromTeacherID]
	toRank, toOK := s.teachers[toTeacherID]
	if !tOK || !fromOK || !toOK {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrNotFound, "teacher or task not found")
	}
	// 换人周必须落在任务周区间内部：至少有一周已上完，且至少剩一周。
	if fromWeek <= task.WeekStart || fromWeek > task.WeekEnd {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrInvalidParameter,
			"fromWeek %d must be within (%d, %d]", fromWeek, task.WeekStart, task.WeekEnd)
	}
	if s.isFrozen(task.Semester, fromTeacherID) || s.isFrozen(task.Semester, toTeacherID) {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "semester already settled")
	}
	old := s.assignments[akey(fromTeacherID, taskID)]
	if old == nil || old.status != StatusConfirmed {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "original teacher has no confirmed assignment on this task")
	}
	if old.weekStart >= fromWeek || fromWeek > old.weekEnd+1 {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState,
			"fromWeek %d outside teacher's current segment [%d,%d]",
			fromWeek, old.weekStart, old.weekEnd)
	}
	if exist := s.assignments[akey(toTeacherID, taskID)]; exist != nil &&
		(exist.status == StatusPending || exist.status == StatusConfirmed) {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "new teacher already assigned to this task")
	}

	// 学时始终按全课程周数均匀分布（base/周），无法整除的零头永远归首任教师，
	// 保证任意换人链路上各教师学时之和恰等于课程学时。
	totalWeeks := task.WeekEnd - task.WeekStart + 1
	base := task.Hours / totalWeeks
	rem := task.Hours % totalWeeks

	// 原教师换人后应保留的学时（相对任务起始周计）。
	// 首任教师拿走零头；非首任（链式换人）当前段只含整数 base。
	var keptHours int
	if old.weekStart == task.WeekStart {
		taughtWeeks := fromWeek - task.WeekStart
		keptHours = taughtWeeks*base + rem
	} else {
		taughtWeeks := fromWeek - old.weekStart
		keptHours = taughtWeeks * base
	}
	newHours := old.hours - keptHours
	if newHours <= 0 || (old.weekStart == task.WeekStart && keptHours <= 0) || keptHours < 0 {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrInvalidParameter, "replace produces invalid split: %d/%d", keptHours, newHours)
	}

	// 新教师冲突校验：只查换入段，同任务其他承担者（合上）豁免。
	occ := s.occ[toTeacherID]
	for wk := fromWeek; wk <= old.weekEnd; wk++ {
		for _, period := range task.Periods {
			if owner, busy := occ[occKey{wk, period}]; busy && owner != taskID {
				s.rollbackSwept(swept, oldNow)
				return errf(ErrSlotConflict, "week %d period %d occupied by %s", wk, period, owner)
			}
		}
	}

	scaleC, newC, labC, _ := taskCoeffs(s.cfg, task)
	oldWorkload := exactWorkload(old.hours, scaleC, newC, labC)
	keptWorkload := exactWorkload(keptHours, scaleC, newC, labC)
	newWorkload := exactWorkload(newHours, scaleC, newC, labC)
	rank := s.ranks[toTeacherID]
	if s.load[toTeacherID]+newWorkload > rank.MaxLoad {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrOverCap, "new teacher would exceed rank max %d", rank.MaxLoad)
	}

	// 全部通过，原子生效。
	s.vacate(old, task)
	s.load[fromTeacherID] -= oldWorkload - keptWorkload
	old.hours = keptHours
	old.weekEnd = fromWeek - 1
	s.occupy(old, task)

	na := &assignment{
		teacherID: toTeacherID,
		taskID:    taskID,
		hours:     newHours,
		status:    StatusConfirmed,
		weekStart: fromWeek,
		weekEnd:   task.WeekEnd,
	}
	s.assignments[akey(toTeacherID, taskID)] = na
	s.occupy(na, task)
	s.load[toTeacherID] += newWorkload
	_ = toRank
	return nil
}
