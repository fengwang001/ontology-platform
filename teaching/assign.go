package teaching

import "sort"

// Assign 批量指派。全有或全无：任一请求不满足则整批拒绝，
// 返回的 *Error.Index 为下标最小的失败项；被拒绝操作不改变任何状态与时钟。
func (s *Service) Assign(reqs []AssignmentReq, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(reqs) == 0 {
		return errf(ErrInvalidParameter, "empty assignment batch")
	}
	for i, r := range reqs {
		if r.TeacherID == "" || r.TaskID == "" || r.Hours <= 0 {
			return errf(ErrInvalidParameter, "req %d has empty id or non-positive hours", i)
		}
	}
	seenPair := map[string]int{}
	for i, r := range reqs {
		k := akey(r.TeacherID, r.TaskID)
		if first, dup := seenPair[k]; dup {
			e := errf(ErrInvalidParameter, "teacher %q repeated for task %q in one batch", r.TeacherID, r.TaskID)
			e.Index = i
			_ = first
			return e
		}
		seenPair[k] = i
	}
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}

	// 校验视图包含“惰性超时释放”效果；若最终拒绝则整体回滚，
	// 保证被拒绝操作不改变任何状态与时钟。
	oldNow := s.now
	s.now = now
	swept := s.sweepExpiredLocked()

	type planned struct {
		req      AssignmentReq
		task     *TaskSpec
		rank     *Rank
		workload int
	}
	plans := make([]planned, len(reqs))

	batchHoursByTask := map[string]int{}
	batchIdxByTask := map[string][]int{}
	for i, r := range reqs {
		batchHoursByTask[r.TaskID] += r.Hours
		batchIdxByTask[r.TaskID] = append(batchIdxByTask[r.TaskID], i)
	}

	var firstErr *Error
	setErr := func(i int, e *Error) {
		if firstErr == nil || i < firstErr.Index ||
			(i == firstErr.Index && higherPriority(e.Code, firstErr.Code)) {
			e.Index = i
			firstErr = e
		}
	}

	for i, r := range reqs {
		task, tOK := s.tasks[r.TaskID]
		_, hOK := s.teachers[r.TeacherID]
		if !tOK || !hOK {
			setErr(i, errf(ErrNotFound, "teacher or task not found"))
			continue
		}
		if s.isFrozen(task.Semester, r.TeacherID) {
			setErr(i, errf(ErrIllegalState, "semester already settled"))
			continue
		}
		if old := s.assignments[akey(r.TeacherID, r.TaskID)]; old != nil &&
			(old.status == StatusPending || old.status == StatusConfirmed) {
			setErr(i, errf(ErrIllegalState, "teacher already assigned to this task"))
			continue
		}
		scaleC, newC, labC, ok := taskCoeffs(s.cfg, task)
		if !ok {
			setErr(i, errf(ErrInvalidParameter, "class size matches no tier"))
			continue
		}
		plans[i] = planned{
			req:      r,
			task:     task,
			rank:     s.ranks[r.TeacherID],
			workload: exactWorkload(r.Hours, scaleC, newC, labC),
		}
	}

	// 学时守恒：被涉及任务的“既有活跃学时 + 本批学时”必须恰等于课程学时。
	taskOrder := make([]string, 0, len(batchHoursByTask))
	for t := range batchHoursByTask {
		taskOrder = append(taskOrder, t)
	}
	sort.Strings(taskOrder)
	for _, tID := range taskOrder {
		task, exists := s.tasks[tID]
		if !exists {
			continue // 已在逐项检查中报 ErrNotFound
		}
		if s.allocated[tID]+batchHoursByTask[tID] != task.Hours {
			idx := batchIdxByTask[tID][0]
			for _, i := range batchIdxByTask[tID] {
				if i < idx {
					idx = i
				}
			}
			setErr(idx, errf(ErrHoursNotConserved,
				"task %s: active %d + batch %d != total %d",
				tID, s.allocated[tID], batchHoursByTask[tID], task.Hours))
		}
	}

	// 时段冲突：逐格 map 查询，复杂度只取决于任务本身周数×节数，
	// 与教师历史任务总数无关。同任务承担者（合上）互相豁免。
	for i := range plans {
		p := plans[i]
		if p.task == nil {
			continue
		}
		occ := s.occ[p.req.TeacherID]
		for wk := p.task.WeekStart; wk <= p.task.WeekEnd; wk++ {
			for _, period := range p.task.Periods {
				if owner, busy := occ[occKey{wk, period}]; busy && owner != p.req.TaskID {
					setErr(i, errf(ErrSlotConflict, "week %d period %d occupied by %s", wk, period, owner))
				}
			}
		}
	}

	// 上限校验：含待确认指派在内，本批累计后不得超职级上限。
	batchLoadByTeacher := map[string]int{}
	for i := range plans {
		p := plans[i]
		if p.task != nil {
			batchLoadByTeacher[p.req.TeacherID] += p.workload
		}
	}
	for i := range plans {
		p := plans[i]
		if p.task == nil {
			continue
		}
		if s.load[p.req.TeacherID]+batchLoadByTeacher[p.req.TeacherID] > p.rank.MaxLoad {
			setErr(i, errf(ErrOverCap, "would exceed rank max %d", p.rank.MaxLoad))
		}
	}

	if firstErr != nil {
		s.rollbackSwept(swept, oldNow)
		return firstErr
	}

	deadline := now + s.cfg.ConfirmTicks
	for i := range plans {
		p := plans[i]
		a := &assignment{
			teacherID: p.req.TeacherID,
			taskID:    p.req.TaskID,
			hours:     p.req.Hours,
			status:    StatusPending,
			deadline:  deadline,
			weekStart: p.task.WeekStart,
			weekEnd:   p.task.WeekEnd,
		}
		s.assignments[akey(p.req.TeacherID, p.req.TaskID)] = a
		s.occupy(a, p.task)
		s.load[p.req.TeacherID] += p.workload
		s.allocated[p.req.TaskID] += p.req.Hours
		s.pushDeadline(a)
	}
	return nil
}

// Respond 教师确认或拒绝待确认指派。恰等于确认期限仍有效；
// 已被超时释放的指派不得再被确认。
func (s *Service) Respond(teacherID, taskID string, accept bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if teacherID == "" || taskID == "" {
		return errf(ErrInvalidParameter, "empty id")
	}
	if now < s.now {
		return errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	oldNow := s.now
	s.now = now
	swept := s.sweepExpiredLocked()

	task, tOK := s.tasks[taskID]
	_, hOK := s.teachers[teacherID]
	if !tOK || !hOK {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrNotFound, "teacher or task not found")
	}
	a := s.assignments[akey(teacherID, taskID)]
	if a == nil || a.status != StatusPending {
		s.rollbackSwept(swept, oldNow)
		return errf(ErrIllegalState, "assignment is not pending")
	}
	if accept {
		a.status = StatusConfirmed
		a.deadline = 0
		return nil
	}
	s.releaseAssignment(a, task)
	a.status = StatusRejected
	return nil
}
