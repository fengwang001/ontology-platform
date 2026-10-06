package teaching

// occupy 把承担段的周×节次格标记为任务占用。
func (s *Service) occupy(a *assignment, task *TaskSpec) {
	m := s.occ[a.teacherID]
	if m == nil {
		m = map[occKey]string{}
		s.occ[a.teacherID] = m
	}
	for wk := a.weekStart; wk <= a.weekEnd; wk++ {
		for _, period := range task.Periods {
			m[occKey{wk, period}] = task.ID
		}
	}
}

// vacate 删除承担段占用（换人时仅退剩余周次）。
func (s *Service) vacate(a *assignment, task *TaskSpec) {
	m := s.occ[a.teacherID]
	for wk := a.weekStart; wk <= a.weekEnd; wk++ {
		for _, period := range task.Periods {
			delete(m, occKey{wk, period})
		}
	}
}

func (s *Service) releaseAssignment(a *assignment, task *TaskSpec) {
	s.vacate(a, task)
	scaleC, newC, labC, _ := taskCoeffs(s.cfg, task)
	s.load[a.teacherID] -= exactWorkload(a.hours, scaleC, newC, labC)
	s.allocated[task.ID] -= a.hours
	a.deadline = 0
}

// sweptEntry 记录一次惰性释放，便于在操作被拒绝时整体回滚。
type sweptEntry struct {
	a        *assignment
	deadline int64
}

// sweepExpiredLocked 惰性释放所有 now > deadline 的待确认指派，
// 返回被释放项以便回滚。严格大于才算超时：恰等于确认期限时仍视为有效。
func (s *Service) sweepExpiredLocked() []*sweptEntry {
	var swept []*sweptEntry
	for len(s.deadlines) > 0 {
		top := s.deadlines[0]
		if top.deadline == 0 || top.status != StatusPending {
			s.popDeadline() // 堆中陈旧记录
			continue
		}
		if s.now <= top.deadline {
			break
		}
		s.popDeadline()
		dl := top.deadline
		task := s.tasks[top.taskID]
		s.releaseAssignment(top, task)
		top.status = StatusReleased
		swept = append(swept, &sweptEntry{a: top, deadline: dl})
	}
	return swept
}

// rollbackSwept 撤销惰性释放（操作最终被拒绝时使用），
// 同时恢复时钟，保证被拒绝操作不留任何痕迹。
func (s *Service) rollbackSwept(swept []*sweptEntry, oldNow int64) {
	for i := len(swept) - 1; i >= 0; i-- {
		e := swept[i]
		a := e.a
		task := s.tasks[a.taskID]
		a.status = StatusPending
		a.deadline = e.deadline
		s.occupy(a, task)
		scaleC, newC, labC, _ := taskCoeffs(s.cfg, task)
		s.load[a.teacherID] += exactWorkload(a.hours, scaleC, newC, labC)
		s.allocated[task.ID] += a.hours
		s.pushDeadline(a)
	}
	s.now = oldNow
}

// pushDeadline 把指派加入按 deadline 升序的最小堆。
func (s *Service) pushDeadline(a *assignment) {
	s.deadlines = append(s.deadlines, a)
	i := len(s.deadlines) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if s.deadlines[parent].deadline <= s.deadlines[i].deadline {
			break
		}
		s.deadlines[parent], s.deadlines[i] = s.deadlines[i], s.deadlines[parent]
		i = parent
	}
}

func (s *Service) popDeadline() {
	n := len(s.deadlines)
	s.deadlines[0] = s.deadlines[n-1]
	s.deadlines = s.deadlines[:n-1]
	i := 0
	for {
		l, r, best := 2*i+1, 2*i+2, i
		if l < len(s.deadlines) && s.deadlines[l].deadline < s.deadlines[best].deadline {
			best = l
		}
		if r < len(s.deadlines) && s.deadlines[r].deadline < s.deadlines[best].deadline {
			best = r
		}
		if best == i {
			return
		}
		s.deadlines[best], s.deadlines[i] = s.deadlines[i], s.deadlines[best]
		i = best
	}
}

func (s *Service) isFrozen(semester, teacherID string) bool {
	if set := s.frozen[semester]; set != nil {
		_, ok := set[teacherID]
		return ok
	}
	return false
}
