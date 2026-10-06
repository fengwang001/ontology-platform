package allocation

// conflict 线性扫描该教师全部历史份额（含已结束的）。
func (m *naiveModel) conflict(teacherID string, start, end int, periods []int) bool {
	for _, sh := range m.shares {
		if sh.teacherID != teacherID ||
			(sh.state != statePending && sh.state != stateActive) {
			continue
		}
		tk := m.tasks[sh.taskID]
		if weeksOverlap(sh.startWeek, sh.endWeek, start, end) &&
			periodsOverlap(tk.Periods, periods) {
			return true
		}
	}
	return false
}

func (m *naiveModel) workloadOf(sh naiveShare) int {
	tk := m.tasks[sh.taskID]
	sp, _ := scalePercent(m.cfg.Tiers, tk.ClassSize)
	wl, _ := taskWorkload(m.cfg, sh.hours, sp, tk.IsNew, tk.IsLab)
	return wl
}

func (m *naiveModel) used(teacherID, semester string) int {
	total := 0
	for _, sh := range m.shares {
		if sh.teacherID != teacherID ||
			m.tasks[sh.taskID].Semester != semester {
			continue
		}
		if sh.state == statePending || sh.state == stateActive {
			total += m.workloadOf(sh)
		}
	}
	return total
}

func (m *naiveModel) addTeacher(now int64, spec TeacherSpec) *Error {
	if spec.ID == "" || spec.Rank == "" {
		return m.fail(ErrInvalidArgument, -1)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.ranks[spec.Rank]; !ok {
		return m.fail(ErrInvalidArgument, -1)
	}
	if _, ok := m.teachers[spec.ID]; ok {
		return m.fail(ErrInvalidArgument, -1)
	}
	m.teachers[spec.ID] = spec.Rank
	m.clock = now
	return nil
}

func (m *naiveModel) addTask(now int64, spec TaskSpec) *Error {
	if spec.ID == "" || spec.Semester == "" || spec.Hours <= 0 || spec.ClassSize < 0 ||
		spec.StartWeek <= 0 || spec.EndWeek < spec.StartWeek || spec.EndWeek > maxWeek ||
		len(spec.Periods) == 0 || spec.Hours < spec.EndWeek-spec.StartWeek+1 {
		return m.fail(ErrInvalidArgument, -1)
	}
	for _, p := range spec.Periods {
		if p <= 0 {
			return m.fail(ErrInvalidArgument, -1)
		}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, ok := m.tasks[spec.ID]; ok {
		return m.fail(ErrInvalidArgument, -1)
	}
	if _, err := scalePercent(m.cfg.Tiers, spec.ClassSize); err != nil {
		return m.fail(ErrInvalidArgument, -1)
	}
	m.tasks[spec.ID] = spec
	m.clock = now
	return nil
}

func (m *naiveModel) taskHasShare(taskID string) bool {
	for _, sh := range m.shares {
		if sh.taskID == taskID {
			return true
		}
	}
	return false
}
