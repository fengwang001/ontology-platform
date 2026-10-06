package allocation

// ChangeTeacher 自 fromWeek 起把剩余课程交给新教师。
//   - 原教师保留 [startWeek, fromWeek-1] 周次（零头学时归原教师，由 splitHours 保证）；
//   - 新教师承担 [fromWeek, endWeek]，立即生效（active，无需再确认）；
//   - 两段学时之和恒等于课程学时；
//   - 新教师须通过冲突与上限校验，任一失败则一切保持原状。
func (s *Service) ChangeTeacher(now int64, shareID int64, newTeacherID string, fromWeek int) (int64, *Error) {
	if shareID <= 0 || newTeacherID == "" {
		return 0, newErr(ErrInvalidArgument, "bad share id or empty teacher")
	}
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return 0, err
	}
	sh, ok := svc.shares[shareID]
	if !ok {
		return 0, newErr(ErrNotFound, "share %d not found", shareID)
	}
	tk := svc.tasks[sh.taskID]
	newTeacher, eok := svc.teachers[newTeacherID]
	if !eok {
		return 0, newErr(ErrNotFound, "teacher %q not found", newTeacherID)
	}
	if sh.state != stateActive && sh.state != statePending {
		return 0, newErr(ErrState, "share %d in state %s cannot change teacher", shareID, sh.state)
	}
	if fromWeek < tk.spec.StartWeek || fromWeek > tk.spec.EndWeek {
		return 0, newErr(ErrInvalidArgument,
			"fromWeek %d out of task weeks [%d,%d]", fromWeek, tk.spec.StartWeek, tk.spec.EndWeek)
	}
	if fromWeek <= sh.startWeek {
		return 0, newErr(ErrInvalidArgument,
			"fromWeek %d must be after the share start week %d (there must be completed weeks)",
			fromWeek, sh.startWeek)
	}
	if newTeacherID == sh.teacherID {
		return 0, newErr(ErrInvalidArgument, "new teacher is the same as current teacher")
	}
	if _, frozen := svc.settled[sh.semester]; frozen {
		return 0, newErr(ErrState, "semester %q settled and frozen", sh.semester)
	}

	// 惰性释放：旧教师若该份额已超时，则先释放并拒绝（状态不允许）。
	oldTeacher := svc.teachers[sh.teacherID]
	svc.expire(oldTeacher, now)
	svc.expire(newTeacher, now)
	if sh.state == stateReleased {
		return 0, newErr(ErrState, "share %d expired and released", shareID)
	}

	// 在该份额当前覆盖的周次区间内切分：旧段 [sh.startWeek, fromWeek-1]，
	// 新段 [fromWeek, sh.endWeek]；零头归任务最早周次，故总是落在旧段一侧。
	oldHours := splitHours(tk.spec.Hours, tk.spec.StartWeek, tk.spec.EndWeek,
		sh.startWeek, fromWeek-1)
	newHours := splitHours(tk.spec.Hours, tk.spec.StartWeek, tk.spec.EndWeek,
		fromWeek, sh.endWeek)

	// 优先级：时段冲突先于超上限，守恒为内部不变量最后断言。
	if newTeacher.sched.overlaps(fromWeek, sh.endWeek, tk.spec.Periods) {
		return 0, newErr(ErrConflict,
			"new teacher %q slot conflict weeks [%d,%d]", newTeacherID, fromWeek, sh.endWeek)
	}
	oldWL, err := taskWorkload(svc.cfg, oldHours, tk.scalePct, tk.spec.IsNew, tk.spec.IsLab)
	if err != nil {
		return 0, err
	}
	newWL, err := taskWorkload(svc.cfg, newHours, tk.scalePct, tk.spec.IsNew, tk.spec.IsLab)
	if err != nil {
		return 0, err
	}
	// 新教师上限校验（计入其现有 pending/active）。
	limit, _ := svc.rankLimit(newTeacher.rank)
	if newTeacher.used[sh.semester]+newWL > limit.Max {
		return 0, newErr(ErrOverCap,
			"new teacher %q projected %d > cap %d", newTeacherID,
			newTeacher.used[sh.semester]+newWL, limit.Max)
	}
	if oldHours+newHours != sh.hours {
		return 0, newErr(ErrHoursConservation,
			"internal split %d+%d != share hours %d", oldHours, newHours, sh.hours)
	}

	// 校验全部通过，原子地变更：旧份额收缩，新份额立即生效。
	tailEndWeek := sh.endWeek
	oldTeacher.sched.remove(sh.startWeek, sh.endWeek, tk.spec.Periods)
	oldTeacher.used[sh.semester] -= sh.workload
	delete(oldTeacher.holding, sh.id)

	sh.endWeek = fromWeek - 1
	sh.hours = oldHours
	sh.workload = oldWL
	sh.state = stateActive
	if oldHours > 0 {
		oldTeacher.sched.add(sh.startWeek, sh.endWeek, tk.spec.Periods)
		oldTeacher.used[sh.semester] += oldWL
		oldTeacher.holding[sh.id] = true
	}

	svc.nextID++
	newShare := &share{
		id: svc.nextID, taskID: sh.taskID, semester: sh.semester, teacherID: newTeacherID,
		hours: newHours, workload: newWL, state: stateActive,
		startWeek: fromWeek, endWeek: tailEndWeek,
		assignedAt: now, deadline: now,
	}
	svc.shares[newShare.id] = newShare
	newTeacher.sched.add(fromWeek, tailEndWeek, tk.spec.Periods)
	newTeacher.used[sh.semester] += newWL
	newTeacher.holding[newShare.id] = true

	svc.clock = now
	svc.trace("ChangeTeacher now=%d share=%d new=%s fromWeek=%d -> oldHours=%d(oldWL=%d) newShare=%d(newHours=%d,newWL=%d) sum=%d",
		now, shareID, newTeacherID, fromWeek, oldHours, oldWL, newShare.id, newHours, newWL, oldHours+newHours)
	return newShare.id, nil
}
