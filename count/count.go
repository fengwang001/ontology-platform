// Package count 实现盘点任务与初盘/复盘/三盘阶段机。
package count

import "ontology/adjust"

// Open 为 1..1000 个互不相同的库位开启盘点；
// 任一库位不存在或已在未关闭任务中，则整体拒绝且不改状态。
func Open(sys *adjust.System, task adjust.ID, locs []adjust.ID) error {
	sys.Lock()
	defer sys.Unlock()

	if !adjust.ValidID(task) || len(locs) < 1 || len(locs) > adjust.MaxCount {
		return adjust.ErrInvalid
	}
	seen := make(map[adjust.ID]struct{}, len(locs))
	for _, loc := range locs {
		if !adjust.ValidID(loc) {
			return adjust.ErrInvalid
		}
		if _, dup := seen[loc]; dup {
			return adjust.ErrInvalid
		}
		seen[loc] = struct{}{}
		l, ok := sys.LocLocked(loc)
		if !ok {
			return adjust.ErrNotFound
		}
		if l.Task != "" {
			return adjust.ErrConflict
		}
	}
	if _, exists := sys.TaskLocked(task); exists {
		return adjust.ErrConflict
	}

	t := &adjust.Task{Locs: seen, LocList: append([]adjust.ID(nil), locs...)}
	sys.PutTaskLocked(task, t)
	for _, loc := range locs {
		l, _ := sys.LocLocked(loc)
		l.Task = task
		l.Phase = adjust.PhaseFirst
		l.C1, l.C2 = 0, 0
		l.M1, l.M2 = 0, 0
		l.P1, l.P2, l.P3 = "", "", ""
		l.Pending = 0
	}
	return nil
}

// Submit 提交某库位本轮实盘数；判定依据为提交瞬间的 book 与容差。
func Submit(sys *adjust.System, task, loc adjust.ID, counted int64, counter adjust.ID) error {
	sys.Lock()
	defer sys.Unlock()

	if !adjust.ValidID(task) || !adjust.ValidID(loc) ||
		!adjust.ValidID(counter) || counted < 0 || counted > adjust.MaxBook {
		return adjust.ErrInvalid
	}
	t, ok := sys.TaskLocked(task)
	if !ok {
		return adjust.ErrNotFound
	}
	l, ok := sys.LocLocked(loc)
	if !ok {
		return adjust.ErrNotFound
	}
	if _, in := t.Locs[loc]; !in {
		return adjust.ErrNotFound
	}
	if t.Closed {
		return adjust.ErrState
	}

	diff := counted - l.Book

	switch l.Phase {
	case adjust.PhaseFirst:
		sys.NoteSubmitLocked(l)
		l.C1, l.P1, l.M1 = counted, counter, l.Mv
		if l.WithinTol(diff, sys.Tpct, sys.Tabs) {
			l.Book = counted
			l.Adj += diff
			l.Phase = adjust.PhaseDone
			return nil
		}
		l.Phase = adjust.PhaseSecond
		return nil

	case adjust.PhaseSecond:
		if counter == l.P1 {
			return adjust.ErrMustSwitch
		}
		sys.NoteSubmitLocked(l)
		l.C2, l.P2, l.M2 = counted, counter, l.Mv
		if l.WithinTol(diff, sys.Tpct, sys.Tabs) {
			l.Book = counted
			l.Adj += diff
			l.Phase = adjust.PhaseDone
			return nil
		}
		// 一致性判定：两次实盘之差恰被期间净移动解释。
		// 只比较两个标量快照，读取移动流水条数为 0（scanned 不增加）。
		sys.NoteConsistencyLocked(l)
		if counted-l.C1 == l.Mv-l.M1 {
			l.Pending = diff
			l.Phase = adjust.PhasePending
			return nil
		}
		l.Phase = adjust.PhaseThird
		return nil

	case adjust.PhaseThird:
		if counter == l.P1 || counter == l.P2 {
			return adjust.ErrMustSwitch
		}
		sys.NoteSubmitLocked(l)
		l.P3 = counter
		if l.WithinTol(diff, sys.Tpct, sys.Tabs) {
			l.Book = counted
			l.Adj += diff
			l.Phase = adjust.PhaseDone
			return nil
		}
		l.Pending = diff
		l.Phase = adjust.PhasePending
		return nil

	default:
		// First/Second/Third 之外的阶段不接受 Submit。
		return adjust.ErrState
	}
}

// Close 关闭任务：要求全部库位 Done；关闭后库位可进入新任务。
func Close(sys *adjust.System, task adjust.ID) error {
	sys.Lock()
	defer sys.Unlock()

	if !adjust.ValidID(task) {
		return adjust.ErrInvalid
	}
	t, ok := sys.TaskLocked(task)
	if !ok {
		return adjust.ErrNotFound
	}
	if t.Closed {
		return adjust.ErrState
	}
	for _, loc := range t.LocList {
		l, _ := sys.LocLocked(loc)
		if l.Phase != adjust.PhaseDone {
			return adjust.ErrState
		}
	}

	t.Closed = true
	for _, loc := range t.LocList {
		l, _ := sys.LocLocked(loc)
		l.Task = ""
		l.Phase = adjust.PhaseIdle
		l.C1, l.C2 = 0, 0
		l.M1, l.M2 = 0, 0
		l.P1, l.P2, l.P3 = "", "", ""
		l.Pending = 0
	}
	return nil
}
