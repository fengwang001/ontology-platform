package allocation

// plannedUse 为一次批量指派建立的临时视图，绝不触碰真实 schedule：
// 任一项失败时没有任何副作用（原子全有或全无）。
type plannedUse struct {
	extraBits map[string]map[int]uint64 // 教师 -> 节次 -> 批次内待加周次位图
	extraWork map[string]int            // "教师\x00学期" -> 批次内待加工作量
}

// AssignBatch 批量指派：全有或全无。按下标升序逐项校验，
// 每项内部严格按错误类别优先级判定；失败时报下标最小的失败项。
func (s *Service) AssignBatch(now int64, items []AssignItem) ([]int64, *Error) {
	for i, it := range items {
		if it.TaskID == "" || it.TeacherID == "" || it.Hours <= 0 {
			return nil, newErrAt(ErrInvalidArgument, i, "bad assign item")
		}
	}
	svc := s.impl
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if err := svc.checkClock(now); err != nil {
		return nil, err
	}

	// 阶段 3（NotFound）：所有项的存在性先于任何状态判定，取最小下标。
	batchHours := make(map[string]int)
	batchTasks := make(map[string]*task)
	for i, it := range items {
		tk, tok := svc.tasks[it.TaskID]
		_, eok := svc.teachers[it.TeacherID]
		if !tok || !eok {
			return nil, newErrAt(ErrNotFound, i, "task/teacher not found: %q %q", it.TaskID, it.TeacherID)
		}
		batchHours[it.TaskID] += it.Hours
		batchTasks[it.TaskID] = tk
	}
	// 阶段 4（State）：学期冻结 / 任务已被指派。
	seenInBatch := make(map[string]bool)
	for i, it := range items {
		tk := batchTasks[it.TaskID]
		if _, frozen := svc.settled[tk.spec.Semester]; frozen {
			return nil, newErrAt(ErrState, i, "semester %q settled and frozen", tk.spec.Semester)
		}
		if !seenInBatch[it.TaskID] && svc.taskHasShare(it.TaskID) {
			return nil, newErrAt(ErrState, i, "task %q already assigned", it.TaskID)
		}
		seenInBatch[it.TaskID] = true
	}

	// 结构判定通过后，惰性释放所有相关教师的超时份额。
	for _, it := range items {
		svc.expire(svc.teachers[it.TeacherID], now)
	}

	// 阶段 5/6/7：单遍计算冲突与上限，但不在首个冲突处提前返回，
	// 从而保证 Conflict 类别（取最小下标）整体先于 OverCap 报告。
	plan := &plannedUse{
		extraBits: map[string]map[int]uint64{},
		extraWork: map[string]int{},
	}
	type built struct {
		sh *share
		tk *task
	}
	result := make([]built, len(items))
	firstConflict := -1
	firstOverCap := -1
	for i, it := range items {
		tk := batchTasks[it.TaskID]
		t := svc.teachers[it.TeacherID]
		mask := weekMask(tk.spec.StartWeek, tk.spec.EndWeek)
		extra := plan.extraBits[it.TeacherID]
		if extra == nil {
			extra = map[int]uint64{}
			plan.extraBits[it.TeacherID] = extra
		}
		conflictHere := false
		for _, p := range tk.spec.Periods {
			if t.sched.bits[p]&mask != 0 || extra[p]&mask != 0 {
				conflictHere = true
				break
			}
		}
		if conflictHere && firstConflict < 0 {
			firstConflict = i
		}
		wl, err := taskWorkload(svc.cfg, it.Hours, tk.scalePct, tk.spec.IsNew, tk.spec.IsLab)
		if err != nil {
			return nil, newErrAt(err.Code, i, "%s", err.Message)
		}
		limit, _ := svc.rankLimit(t.rank)
		key := it.TeacherID + "\x00" + tk.spec.Semester
		projected := t.used[tk.spec.Semester] + plan.extraWork[key] + wl
		if projected > limit.Max && firstOverCap < 0 {
			firstOverCap = i
		}
		for _, p := range tk.spec.Periods {
			extra[p] |= mask
		}
		plan.extraWork[key] += wl
		result[i] = built{
			sh: &share{
				taskID: it.TaskID, semester: tk.spec.Semester, teacherID: it.TeacherID,
				hours: it.Hours, workload: wl, state: statePending,
				startWeek: tk.spec.StartWeek, endWeek: tk.spec.EndWeek,
				assignedAt: now, deadline: now + svc.cfg.ConfirmDeadline,
			},
			tk: tk,
		}
	}
	if firstConflict >= 0 {
		i := firstConflict
		it := items[i]
		tk := batchTasks[it.TaskID]
		return nil, newErrAt(ErrConflict, i,
			"teacher %q slot conflict on task %q weeks [%d,%d]",
			it.TeacherID, it.TaskID, tk.spec.StartWeek, tk.spec.EndWeek)
	}
	if firstOverCap >= 0 {
		i := firstOverCap
		it := items[i]
		limit, _ := svc.rankLimit(svc.teachers[it.TeacherID].rank)
		return nil, newErrAt(ErrOverCap, i,
			"teacher %q projected workload > cap %d", it.TeacherID, limit.Max)
	}

	// 阶段 8（ErrHoursConservation）：最后判定守恒。
	for taskID, total := range batchHours {
		tk := batchTasks[taskID]
		if total != tk.spec.Hours {
			for i, it := range items {
				if it.TaskID == taskID {
					return nil, newErrAt(ErrHoursConservation, i,
						"task %q co-teach hours sum %d != total %d", taskID, total, tk.spec.Hours)
				}
			}
		}
	}

	// 全部通过：原子提交。
	ids := make([]int64, len(items))
	for i, b := range result {
		svc.nextID++
		b.sh.id = svc.nextID
		svc.shares[b.sh.id] = b.sh
		t := svc.teachers[b.sh.teacherID]
		t.sched.add(b.sh.startWeek, b.sh.endWeek, b.tk.spec.Periods)
		t.holding[b.sh.id] = true
		t.used[b.sh.semester] += b.sh.workload
		ids[i] = b.sh.id
		svc.trace("AssignBatch now=%d idx=%d item={task:%s teacher:%s hours:%d} -> share=%d wl=%d deadline=%d",
			now, i, items[i].TaskID, items[i].TeacherID, items[i].Hours,
			b.sh.id, b.sh.workload, b.sh.deadline)
	}
	svc.clock = now
	return ids, nil
}

func (svc *internalService) taskHasShare(taskID string) bool {
	for _, sh := range svc.shares {
		if sh.taskID == taskID {
			return true
		}
	}
	return false
}
