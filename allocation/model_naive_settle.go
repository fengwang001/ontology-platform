package allocation

import "sort"

func (m *naiveModel) settle(now int64, semester string) (map[string]naiveTeacherResult, bool, *Error) {
	if semester == "" {
		return nil, false, m.fail(ErrInvalidArgument, -1)
	}
	if err := m.checkClock(now); err != nil {
		return nil, false, err
	}
	if r, ok := m.settled[semester]; ok {
		return r, true, nil
	}
	m.expireAll(now)
	var pred string
	var predAt int64 = -1
	for sem, at := range m.settledAt {
		if at > predAt {
			predAt = at
			pred = sem
		}
	}
	out := map[string]naiveTeacherResult{}
	ids := make([]string, 0, len(m.teachers))
	for id := range m.teachers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		limit := m.ranks[m.teachers[id]]
		total := m.used(id, semester)
		deficit := maxInt(0, limit.Min-total)
		excess := maxInt(0, total-limit.Min)
		cap := limit.Min / 4
		brought := 0
		if pred != "" {
			if pr, ok := m.settled[pred][id]; ok {
				brought = pr.next
			}
		}
		used := minInt(minInt(brought, deficit), cap)
		out[id] = naiveTeacherResult{
			rank: m.teachers[id], total: total, min: limit.Min, max: limit.Max,
			deficit: deficit, excess: excess, brought: brought, used: used,
			unmet: deficit - used, next: minInt(excess/2, cap),
		}
	}
	m.settled[semester] = out
	m.settledAt[semester] = now
	m.clock = now
	return out, false, nil
}

// shareSignature 生成份额集合的规范摘要（与 ID 无关的守恒视图）。
func (m *naiveModel) shareSignature() []string {
	type row struct {
		task, teacher, state string
		s, e, hours          int
	}
	var rows []row
	for _, sh := range m.shares {
		rows = append(rows, row{
			sh.taskID, sh.teacherID, sh.state,
			sh.startWeek, sh.endWeek, sh.hours,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].task != rows[j].task {
			return rows[i].task < rows[j].task
		}
		if rows[i].teacher != rows[j].teacher {
			return rows[i].teacher < rows[j].teacher
		}
		if rows[i].s != rows[j].s {
			return rows[i].s < rows[j].s
		}
		return rows[i].hours < rows[j].hours
	})
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.task + "/" + r.teacher + "/" + r.state +
			itox(r.s) + "-" + itox(r.e) + ":h" + itox(r.hours)
	}
	return out
}

func itox(i int) string {
	return string(rune('a'+i%26)) + "_" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [24]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
