package allocation

import "sort"

// Settle 核算一个学期：
//   - 幂等：已核算则原样返回同一份结果，时钟不变；
//   - 先惰性释放所有已超时的待确认份额（恰等期限仍有效并计入）；
//   - 欠额 = max(0, 下限-累计)，超额 = max(0, 累计-下限)；
//   - 上学期结转抵扣按 min(可用抵扣, 欠额, floor(本学期下限/4)) 冲抵，剩余为未达标；
//   - 本学期生成的下学期抵扣 = min(floor(超额/2), floor(下限/4))；
//   - 核算后该学期冻结，任何指派/换人均被拒绝。
func (s *Service) Settle(now int64, semester string) (*SemesterReport, *Error) {
	if semester == "" {
		return nil, newErr(ErrInvalidArgument, "empty semester")
	}
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return nil, err
	}
	if rep, ok := svc.settled[semester]; ok {
		svc.trace("Settle now=%d semester=%s -> idempotent cached settledAt=%d", now, semester, rep.SettledAt)
		return cloneReport(rep), nil
	}

	// 惰性释放全部超时待确认份额。
	for _, t := range svc.teachers {
		svc.expire(t, now)
	}

	predecessor := svc.predecessorSemester(semester)
	rep := &SemesterReport{Semester: semester, SettledAt: now}

	teacherIDs := make([]string, 0, len(svc.teachers))
	for id := range svc.teachers {
		teacherIDs = append(teacherIDs, id)
	}
	sort.Strings(teacherIDs)
	for _, id := range teacherIDs {
		t := svc.teachers[id]
		limit, _ := svc.rankLimit(t.rank)
		total := t.used[semester]
		deficit := maxInt(0, limit.Min-total)
		excess := maxInt(0, total-limit.Min)

		brought, cap := 0, limit.Min/4
		if predecessor != "" {
			if prev, ok := svc.settled[predecessor]; ok {
				for _, pr := range prev.Teachers {
					if pr.TeacherID == id {
						brought = pr.CreditNext
						break
					}
				}
			}
		}
		used := minInt(minInt(brought, deficit), cap)
		used = minInt(used, cap)
		unmet := deficit - used
		creditNext := minInt(excess/2, cap)

		rep.Teachers = append(rep.Teachers, TeacherReport{
			TeacherID: id, Rank: t.rank, Total: total,
			Min: limit.Min, Max: limit.Max,
			Deficit: deficit, Excess: excess,
			CreditBrought: brought, CreditUsed: used,
			Unmet: unmet, CreditNext: creditNext,
		})
		svc.trace("Settle teacher=%s total=%d[%d,%d] deficit=%d excess=%d brought=%d cap=%d used=%d unmet=%d creditNext=%d",
			id, total, limit.Min, limit.Max, deficit, excess, brought, cap, used, unmet, creditNext)
	}

	svc.settled[semester] = rep
	svc.clock = now
	svc.trace("Settle now=%d semester=%s -> frozen, predecessor=%q", now, semester, predecessor)
	return cloneReport(rep), nil
}

// predecessorSemester 以已核算学期的 SettledAt 先后定义"上一学期"：
// 即截止当前时间之前最近一次完成核算的学期。
func (svc *internalService) predecessorSemester(semester string) string {
	var best string
	var bestAt int64 = -1
	for sem, rep := range svc.settled {
		if sem == semester {
			continue
		}
		if rep.SettledAt > bestAt {
			bestAt = rep.SettledAt
			best = sem
		}
	}
	return best
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cloneReport(r *SemesterReport) *SemesterReport {
	cp := *r
	cp.Teachers = append([]TeacherReport(nil), r.Teachers...)
	return &cp
}
