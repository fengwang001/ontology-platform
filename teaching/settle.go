package teaching

// Settle 对某教师某学期做期末核算。幂等：重复核算返回完全相同的结果。
// 核算时该教师仍待确认的指派先被惰性超时逻辑处理，再按已生效量核算。
// 核算后该 (学期, 教师) 立即冻结，其该学期指派不允许再变更。
func (s *Service) Settle(teacherID, semester string, now int64) (*SettlementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if teacherID == "" || semester == "" {
		return nil, errf(ErrInvalidParameter, "empty id")
	}
	if now < s.now {
		return nil, errf(ErrClockRollback, "now %d < %d", now, s.now)
	}
	k := ckey(semester, teacherID)
	if r, ok := s.settlements[k]; ok {
		return r, nil // 幂等：重复核算原样返回，不推进时钟、不触发释放
	}
	s.now = now
	s.sweepExpiredLocked()

	if _, hOK := s.teachers[teacherID]; !hOK {
		return nil, errf(ErrNotFound, "teacher not found")
	}
	// 期末核算语义上晚于一切确认期限：该教师本学期仍待确认的指派一律释放，
	// 不计入工作量，也不再占用时段与上限。
	for _, a := range s.assignments {
		if a.teacherID == teacherID && a.status == StatusPending &&
			s.tasks[a.taskID].Semester == semester {
			s.releaseAssignment(a, s.tasks[a.taskID])
			a.status = StatusReleased
		}
	}

	rank := s.ranks[teacherID]
	load := 0
	for _, a := range s.assignments {
		if a.teacherID != teacherID || a.status != StatusConfirmed {
			continue
		}
		task := s.tasks[a.taskID]
		if task.Semester != semester {
			continue
		}
		scaleC, newC, labC, _ := taskCoeffs(s.cfg, task)
		load += exactWorkload(a.hours, scaleC, newC, labC)
	}

	incoming := s.credits[k] // 上学期结算时写入的结转额度
	shortfall := rank.MinLoad - load
	if shortfall < 0 {
		shortfall = 0
	}
	excess := load - rank.MinLoad
	if excess < 0 {
		excess = 0
	}

	// 抵扣只能冲抵欠额，且本学期可用抵扣总量封顶为下限的 1/4（向下取整）。
	cap := rank.MinLoad / 4
	used := incoming
	if used > shortfall {
		used = shortfall
	}
	if used > cap {
		used = cap
	}
	unmet := shortfall - used

	// 超额一半（向下取整）结转为下学期可抵扣额度；超出部分作废。
	earned := excess / 2
	if s.frozen[semester] == nil {
		s.frozen[semester] = map[string]struct{}{}
	}
	s.frozen[semester][teacherID] = struct{}{}

	res := &SettlementResult{
		TeacherID:      teacherID,
		Semester:       semester,
		Load:           load,
		MinLoad:        rank.MinLoad,
		Shortfall:      shortfall,
		Excess:         excess,
		CreditEarned:   earned,
		CreditUsed:     used,
		Unmet:          unmet,
		CreditIncoming: incoming,
	}
	s.settlements[k] = res
	s.credits[NextSemester(semester)+"\x00"+teacherID] = earned
	// 本学期未用完的结转额度（含被 1/4 封顶作废部分）一律作废。
	delete(s.credits, k)
	return res, nil
}

// NextSemester 返回下一学期字符串。支持 "2024-1"/"2024-2"、"2024A"/"2024B"、
// "2024S"/"2024F" 等常见写法；无法解析时退化为 name+"-next"。
// 学期名只承担“先后顺序”语义，具体编码约定见设计说明。
func NextSemester(semester string) string {
	if len(semester) < 2 {
		return semester + "-next"
	}
	last := semester[len(semester)-1]
	prefix := semester[:len(semester)-1]
	switch last {
	case '1':
		return prefix + "2"
	case '2':
		return incDigits(prefix) + "1"
	case 'A', 'a', 'S', 's':
		return prefix + string(rune(last+1))
	case 'B', 'b', 'F', 'f':
		return incDigits(prefix) + string(last-1)
	}
	return semester + "-next"
}

func incDigits(s string) string {
	b := []byte(s)
	carry := true
	for i := len(b) - 1; i >= 0 && carry; i-- {
		if b[i] >= '0' && b[i] <= '9' {
			if b[i] == '9' {
				b[i] = '0'
			} else {
				b[i]++
				carry = false
			}
		}
	}
	if carry {
		return "1" + string(b)
	}
	return string(b)
}
