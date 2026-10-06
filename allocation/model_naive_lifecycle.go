package allocation

func (m *naiveModel) assignBatch(now int64, items []AssignItem) *Error {
	for i, it := range items {
		if it.TaskID == "" || it.TeacherID == "" || it.Hours <= 0 {
			return m.fail(ErrInvalidArgument, i)
		}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	// 阶段 3：存在性（取最小下标）。
	batchHours := map[string]int{}
	for i, it := range items {
		_, tok := m.tasks[it.TaskID]
		_, eok := m.teachers[it.TeacherID]
		if !tok || !eok {
			return m.fail(ErrNotFound, i)
		}
		batchHours[it.TaskID] += it.Hours
	}
	// 阶段 4：冻结 / 已指派。
	seen := map[string]bool{}
	for i, it := range items {
		tk := m.tasks[it.TaskID]
		if m.settled[tk.Semester] != nil {
			return m.fail(ErrState, i)
		}
		if !seen[it.TaskID] && m.taskHasShare(it.TaskID) {
			return m.fail(ErrState, i)
		}
		seen[it.TaskID] = true
	}
	touched := map[string]bool{}
	for _, it := range items {
		touched[it.TeacherID] = true
	}
	for teacherID := range touched {
		m.expireTeacher(teacherID, now)
	}

	// 阶段 5/6：冲突与上限，类别优先（冲突整体先报，取最小下标）。
	type planned struct {
		sh naiveShare
		wl int
	}
	var built []planned
	extraWork := map[string]int{}
	firstConflict, firstOverCap := -1, -1
	for i, it := range items {
		tk := m.tasks[it.TaskID]
		if m.conflict(it.TeacherID, tk.StartWeek, tk.EndWeek, tk.Periods) {
			if firstConflict < 0 {
				firstConflict = i
			}
		}
		for _, p := range built {
			if p.sh.teacherID != it.TeacherID {
				continue
			}
			etk := m.tasks[p.sh.taskID]
			if weeksOverlap(p.sh.startWeek, p.sh.endWeek, tk.StartWeek, tk.EndWeek) &&
				periodsOverlap(etk.Periods, tk.Periods) {
				if firstConflict < 0 {
					firstConflict = i
				}
			}
		}
		sp, _ := scalePercent(m.cfg.Tiers, tk.ClassSize)
		wl, err := taskWorkload(m.cfg, it.Hours, sp, tk.IsNew, tk.IsLab)
		if err != nil {
			return m.fail(err.Code, i)
		}
		rank := m.ranks[m.teachers[it.TeacherID]]
		key := it.TeacherID + "|" + tk.Semester
		if m.used(it.TeacherID, tk.Semester)+extraWork[key]+wl > rank.Max {
			if firstOverCap < 0 {
				firstOverCap = i
			}
		}
		extraWork[key] += wl
		built = append(built, planned{
			sh: naiveShare{
				taskID: it.TaskID, teacherID: it.TeacherID, hours: it.Hours,
				state:     statePending,
				startWeek: tk.StartWeek, endWeek: tk.EndWeek,
				assignedAt: now, deadline: now + m.cfg.ConfirmDeadline,
			},
			wl: wl,
		})
	}
	if firstConflict >= 0 {
		return m.fail(ErrConflict, firstConflict)
	}
	if firstOverCap >= 0 {
		return m.fail(ErrOverCap, firstOverCap)
	}
	// 阶段 8：守恒最后判定。
	for taskID, h := range batchHours {
		if h != m.tasks[taskID].Hours {
			for i, it := range items {
				if it.TaskID == taskID {
					return m.fail(ErrHoursConservation, i)
				}
			}
		}
	}
	for _, p := range built {
		m.nextID++
		p.sh.id = m.nextID
		m.shares = append(m.shares, p.sh)
	}
	m.clock = now
	return nil
}

func (m *naiveModel) respond(now, id int64, accept bool) *Error {
	if id <= 0 {
		return m.fail(ErrInvalidArgument, -1)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	idx := -1
	for i := range m.shares {
		if m.shares[i].id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return m.fail(ErrNotFound, -1)
	}
	m.expireTeacher(m.shares[idx].teacherID, now)
	switch m.shares[idx].state {
	case statePending:
		if accept {
			m.shares[idx].state = stateActive
		} else {
			m.shares[idx].state = stateRejected
		}
		m.clock = now
		return nil
	default:
		return m.fail(ErrState, -1)
	}
}

func (m *naiveModel) changeTeacher(now int64, shareID int64, newTeacherID string, fromWeek int) *Error {
	if shareID <= 0 || newTeacherID == "" {
		return m.fail(ErrInvalidArgument, -1)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	idx := -1
	for i := range m.shares {
		if m.shares[i].id == shareID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return m.fail(ErrNotFound, -1)
	}
	if _, ok := m.teachers[newTeacherID]; !ok {
		return m.fail(ErrNotFound, -1)
	}
	old := &m.shares[idx]
	tk := m.tasks[old.taskID]
	if old.state != stateActive && old.state != statePending {
		return m.fail(ErrState, -1)
	}
	if fromWeek < tk.StartWeek || fromWeek > tk.EndWeek || newTeacherID == old.teacherID ||
		fromWeek <= old.startWeek {
		return m.fail(ErrInvalidArgument, -1)
	}
	if m.settled[tk.Semester] != nil {
		return m.fail(ErrState, -1)
	}
	m.expireTeacher(old.teacherID, now)
	m.expireTeacher(newTeacherID, now)
	if old.state == stateReleased {
		return m.fail(ErrState, -1)
	}
	oldHours := splitHours(tk.Hours, tk.StartWeek, tk.EndWeek, old.startWeek, fromWeek-1)
	newHours := splitHours(tk.Hours, tk.StartWeek, tk.EndWeek, fromWeek, old.endWeek)
	if m.conflict(newTeacherID, fromWeek, old.endWeek, tk.Periods) {
		return m.fail(ErrConflict, -1)
	}
	sp, _ := scalePercent(m.cfg.Tiers, tk.ClassSize)
	newWL, _ := taskWorkload(m.cfg, newHours, sp, tk.IsNew, tk.IsLab)
	rank := m.ranks[m.teachers[newTeacherID]]
	if m.used(newTeacherID, tk.Semester)+newWL > rank.Max {
		return m.fail(ErrOverCap, -1)
	}
	if oldHours+newHours != old.hours {
		return m.fail(ErrHoursConservation, -1)
	}
	tailEnd := old.endWeek
	old.hours = oldHours
	old.endWeek = fromWeek - 1
	old.state = stateActive
	m.nextID++
	m.shares = append(m.shares, naiveShare{
		id: m.nextID, taskID: old.taskID, teacherID: newTeacherID,
		hours: newHours, state: stateActive,
		startWeek: fromWeek, endWeek: tailEnd,
		assignedAt: now, deadline: now,
	})
	m.clock = now
	return nil
}
