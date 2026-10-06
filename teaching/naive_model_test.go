package teaching

import "sort"

// 本文件包含独立编写的朴素参考模型：直接遍历教师全部指派、对每个任务
// 逐周逐节扫描冲突。它刻意不共享生产代码的任何内部数据结构与索引
// （只复用公开类型与纯函数语义），用于随机操作序列的差分对照。

type naiveAssignment struct {
	hours              int
	status             string
	deadline           int64
	weekStart, weekEnd int
}

type naiveModel struct {
	cfg     Config
	now     int64
	ranks   map[string]string
	tasks   map[string]*TaskSpec
	assigns map[string]*naiveAssignment
	sorted  []string
	frozen  map[string]map[string]struct{}
	settled map[string]*SettlementResult
	credits map[string]int
}

func newNaiveModel(cfg Config, now int64) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		now:     now,
		ranks:   map[string]string{},
		tasks:   map[string]*TaskSpec{},
		assigns: map[string]*naiveAssignment{},
		frozen:  map[string]map[string]struct{}{},
		settled: map[string]*SettlementResult{},
		credits: map[string]int{},
	}
}

// clone 深拷贝全部状态。朴素模型在每个可能失败的操作前克隆，
// 失败即整体丢弃，天然实现“拒绝不改变任何状态与时钟”。
func (m *naiveModel) clone() *naiveModel {
	c := &naiveModel{
		cfg:     m.cfg,
		now:     m.now,
		ranks:   map[string]string{},
		tasks:   map[string]*TaskSpec{},
		assigns: map[string]*naiveAssignment{},
		frozen:  map[string]map[string]struct{}{},
		settled: map[string]*SettlementResult{},
		credits: map[string]int{},
	}
	for k, v := range m.ranks {
		c.ranks[k] = v
	}
	for k, v := range m.tasks {
		cp := *v
		cp.Periods = append([]int(nil), v.Periods...)
		c.tasks[k] = &cp
	}
	for _, k := range m.sorted {
		a := *m.assigns[k]
		c.assigns[k] = &a
		c.sorted = append(c.sorted, k)
	}
	for sem, set := range m.frozen {
		c.frozen[sem] = map[string]struct{}{}
		for t := range set {
			c.frozen[sem][t] = struct{}{}
		}
	}
	for k, v := range m.settled {
		r := *v
		c.settled[k] = &r
	}
	for k, v := range m.credits {
		c.credits[k] = v
	}
	return c
}

func (m *naiveModel) commit(c *naiveModel) { *m = *c }

func (m *naiveModel) isFrozen(sem, teacher string) bool {
	if set := m.frozen[sem]; set != nil {
		_, ok := set[teacher]
		return ok
	}
	return false
}

func (m *naiveModel) workload(hours int, t *TaskSpec) int {
	sc, nc, lc, _ := taskCoeffs(m.cfg, t)
	return exactWorkload(hours, sc, nc, lc)
}

func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// sweep 朴素超时处理：线性遍历全部指派（显式 O(历史总数)）。
func (m *naiveModel) sweep() {
	for _, k := range m.sorted {
		a := m.assigns[k]
		if a.status == StatusPending && m.now > a.deadline {
			a.status = StatusReleased
			a.deadline = 0
		}
	}
}

// loadOf 朴素累计：遍历教师全部活跃指派。
func (m *naiveModel) loadOf(teacher string) int {
	total := 0
	for _, k := range m.sorted {
		th, tid := splitKey(k)
		a := m.assigns[k]
		if th != teacher || (a.status != StatusPending && a.status != StatusConfirmed) {
			continue
		}
		total += m.workload(a.hours, m.tasks[tid])
	}
	return total
}

// conflict 朴素冲突：遍历教师历史全部指派做周×节重叠检查。
// 同任务承担者豁免；复杂度显式随历史任务总数增长。
func (m *naiveModel) conflict(teacher string, t *TaskSpec, ws, we int) bool {
	for _, k := range m.sorted {
		th, tid := splitKey(k)
		a := m.assigns[k]
		if th != teacher || (a.status != StatusPending && a.status != StatusConfirmed) || tid == t.ID {
			continue
		}
		other := m.tasks[tid]
		for wk := ws; wk <= we; wk++ {
			if wk < a.weekStart || wk > a.weekEnd {
				continue
			}
			for _, p := range t.Periods {
				for _, op := range other.Periods {
					if p == op {
						return true
					}
				}
			}
		}
	}
	return false
}

func (m *naiveModel) assignedHours(tid string) int {
	total := 0
	for _, k := range m.sorted {
		_, t := splitKey(k)
		a := m.assigns[k]
		if t == tid && (a.status == StatusPending || a.status == StatusConfirmed) {
			total += a.hours
		}
	}
	return total
}

func nerr(code ErrorCode, msg string) *Error {
	return &Error{Code: code, Index: -1, Msg: msg}
}

func (m *naiveModel) addTeacher(id, rank string, now int64) *Error {
	if id == "" || rank == "" {
		return nerr(ErrInvalidParameter, "empty")
	}
	if now < m.now {
		return nerr(ErrClockRollback, "rb")
	}
	c := m.clone()
	c.now = now
	c.sweep()
	if _, ok := c.cfg.Ranks[rank]; !ok {
		return nerr(ErrInvalidParameter, "rank")
	}
	if _, dup := c.ranks[id]; dup {
		return nerr(ErrIllegalState, "dup")
	}
	c.ranks[id] = rank
	m.commit(c)
	return nil
}

func (m *naiveModel) addTask(spec TaskSpec, now int64) *Error {
	if err := validateTaskSpec(spec); err != nil {
		return err.(*Error)
	}
	if now < m.now {
		return nerr(ErrClockRollback, "rb")
	}
	c := m.clone()
	c.now = now
	c.sweep()
	if _, ok := scaleCoeff(c.cfg.Tiers, spec.ClassSize); !ok {
		return nerr(ErrInvalidParameter, "tier")
	}
	if _, dup := c.tasks[spec.ID]; dup {
		return nerr(ErrIllegalState, "dup")
	}
	cp := spec
	cp.Periods = append([]int(nil), spec.Periods...)
	sort.Ints(cp.Periods)
	c.tasks[spec.ID] = &cp
	m.commit(c)
	return nil
}

func (m *naiveModel) assign(reqs []AssignmentReq, now int64) *Error {
	if len(reqs) == 0 {
		return nerr(ErrInvalidParameter, "empty")
	}
	for i, r := range reqs {
		if r.TeacherID == "" || r.TaskID == "" || r.Hours <= 0 {
			e := nerr(ErrInvalidParameter, "bad req")
			e.Index = i
			return e
		}
	}
	seenPair := map[string]int{}
	for i, r := range reqs {
		k := akey(r.TeacherID, r.TaskID)
		if _, dup := seenPair[k]; dup {
			e := nerr(ErrInvalidParameter, "repeat pair")
			e.Index = i
			return e
		}
		seenPair[k] = i
	}
	if now < m.now {
		return nerr(ErrClockRollback, "rb")
	}

	c := m.clone()
	c.now = now
	c.sweep()

	batchHours := map[string]int{}
	batchIdx := map[string]int{}
	for i, r := range reqs {
		batchHours[r.TaskID] += r.Hours
		if _, ok := batchIdx[r.TaskID]; !ok || i < batchIdx[r.TaskID] {
			batchIdx[r.TaskID] = i
		}
	}

	first := -1
	var best *Error
	consider := func(i int, e *Error) {
		if best == nil || i < first || (i == first && higherPriority(e.Code, best.Code)) {
			first = i
			best = e
		}
	}

	planOK := make([]bool, len(reqs))
	planW := make([]int, len(reqs))
	for i, r := range reqs {
		t, tOK := c.tasks[r.TaskID]
		_, hOK := c.ranks[r.TeacherID]
		if !tOK || !hOK {
			consider(i, nerr(ErrNotFound, "nf"))
			continue
		}
		if c.isFrozen(t.Semester, r.TeacherID) {
			consider(i, nerr(ErrIllegalState, "frozen"))
			continue
		}
		if a := c.assigns[akey(r.TeacherID, r.TaskID)]; a != nil &&
			(a.status == StatusPending || a.status == StatusConfirmed) {
			consider(i, nerr(ErrIllegalState, "dup"))
			continue
		}
		planOK[i] = true
		planW[i] = c.workload(r.Hours, t)
	}

	tids := make([]string, 0, len(batchHours))
	for t := range batchHours {
		tids = append(tids, t)
	}
	sort.Strings(tids)
	for _, tid := range tids {
		t, ok := c.tasks[tid]
		if !ok {
			continue
		}
		if c.assignedHours(tid)+batchHours[tid] != t.Hours {
			consider(batchIdx[tid], nerr(ErrHoursNotConserved, "conservation"))
		}
	}

	for i, r := range reqs {
		if planOK[i] && c.conflict(r.TeacherID, c.tasks[r.TaskID],
			c.tasks[r.TaskID].WeekStart, c.tasks[r.TaskID].WeekEnd) {
			consider(i, nerr(ErrSlotConflict, "conflict"))
		}
	}

	batchLoad := map[string]int{}
	for i, r := range reqs {
		if planOK[i] {
			batchLoad[r.TeacherID] += planW[i]
		}
	}
	for i, r := range reqs {
		if !planOK[i] {
			continue
		}
		rank := c.cfg.Ranks[c.ranks[r.TeacherID]]
		if c.loadOf(r.TeacherID)+batchLoad[r.TeacherID] > rank.MaxLoad {
			consider(i, nerr(ErrOverCap, "cap"))
		}
	}

	if best != nil {
		best.Index = first
		return best
	}

	for _, r := range reqs {
		t := c.tasks[r.TaskID]
		a := &naiveAssignment{
			hours:     r.Hours,
			status:    StatusPending,
			deadline:  now + c.cfg.ConfirmTicks,
			weekStart: t.WeekStart,
			weekEnd:   t.WeekEnd,
		}
		k := akey(r.TeacherID, r.TaskID)
		if _, exists := c.assigns[k]; !exists {
			c.sorted = append(c.sorted, k)
		}
		c.assigns[k] = a
	}
	m.commit(c)
	return nil
}

func (m *naiveModel) respond(teacher, task string, accept bool, now int64) *Error {
	if teacher == "" || task == "" {
		return nerr(ErrInvalidParameter, "empty")
	}
	if now < m.now {
		return nerr(ErrClockRollback, "rb")
	}
	c := m.clone()
	c.now = now
	c.sweep()
	_, tOK := c.tasks[task]
	_, hOK := c.ranks[teacher]
	if !tOK || !hOK {
		return nerr(ErrNotFound, "nf")
	}
	a := c.assigns[akey(teacher, task)]
	if a == nil || a.status != StatusPending {
		return nerr(ErrIllegalState, "state")
	}
	if accept {
		a.status = StatusConfirmed
		a.deadline = 0
	} else {
		a.status = StatusRejected
		a.deadline = 0
	}
	m.commit(c)
	return nil
}

func (m *naiveModel) replace(from, task, to string, fromWeek int, now int64) *Error {
	if from == "" || task == "" || to == "" {
		return nerr(ErrInvalidParameter, "empty")
	}
	if from == to {
		return nerr(ErrInvalidParameter, "same")
	}
	if now < m.now {
		return nerr(ErrClockRollback, "rb")
	}
	c := m.clone()
	c.now = now
	c.sweep()
	t, tOK := c.tasks[task]
	_, fOK := c.ranks[from]
	_, toOK := c.ranks[to]
	if !tOK || !fOK || !toOK {
		return nerr(ErrNotFound, "nf")
	}
	if fromWeek <= t.WeekStart || fromWeek > t.WeekEnd {
		return nerr(ErrInvalidParameter, "week")
	}
	if c.isFrozen(t.Semester, from) || c.isFrozen(t.Semester, to) {
		return nerr(ErrIllegalState, "frozen")
	}
	old := c.assigns[akey(from, task)]
	if old == nil || old.status != StatusConfirmed {
		return nerr(ErrIllegalState, "state")
	}
	if old.weekStart >= fromWeek || fromWeek > old.weekEnd+1 {
		return nerr(ErrIllegalState, "segment")
	}
	if a := c.assigns[akey(to, task)]; a != nil &&
		(a.status == StatusPending || a.status == StatusConfirmed) {
		return nerr(ErrIllegalState, "dup")
	}

	totalWeeks := t.WeekEnd - t.WeekStart + 1
	base := t.Hours / totalWeeks
	rem := t.Hours % totalWeeks
	var kept int
	if old.weekStart == t.WeekStart {
		kept = (fromWeek-t.WeekStart)*base + rem
	} else {
		kept = (fromWeek - old.weekStart) * base
	}
	newHours := old.hours - kept
	if newHours <= 0 || (old.weekStart == t.WeekStart && kept <= 0) || kept < 0 {
		return nerr(ErrInvalidParameter, "split")
	}
	if c.conflict(to, t, fromWeek, old.weekEnd) {
		return nerr(ErrSlotConflict, "conflict")
	}
	rank := c.cfg.Ranks[c.ranks[to]]
	if c.loadOf(to)+c.workload(newHours, t) > rank.MaxLoad {
		return nerr(ErrOverCap, "cap")
	}

	old.hours = kept
	old.weekEnd = fromWeek - 1
	na := &naiveAssignment{
		hours:     newHours,
		status:    StatusConfirmed,
		weekStart: fromWeek,
		weekEnd:   t.WeekEnd,
	}
	k := akey(to, task)
	if _, exists := c.assigns[k]; !exists {
		c.sorted = append(c.sorted, k)
	}
	c.assigns[k] = na
	m.commit(c)
	return nil
}

func (m *naiveModel) settle(teacher, semester string, now int64) (*SettlementResult, *Error) {
	if teacher == "" || semester == "" {
		return nil, nerr(ErrInvalidParameter, "empty")
	}
	if now < m.now {
		return nil, nerr(ErrClockRollback, "rb")
	}
	k := ckey(semester, teacher)
	if r, ok := m.settled[k]; ok {
		return r, nil
	}
	c := m.clone()
	c.now = now
	c.sweep()
	if _, ok := c.ranks[teacher]; !ok {
		return nil, nerr(ErrNotFound, "nf")
	}
	for _, ak := range c.sorted {
		th, tid := splitKey(ak)
		a := c.assigns[ak]
		if th == teacher && a.status == StatusPending && c.tasks[tid].Semester == semester {
			a.status = StatusReleased
			a.deadline = 0
		}
	}
	rank := c.cfg.Ranks[c.ranks[teacher]]
	load := 0
	for _, ak := range c.sorted {
		th, tid := splitKey(ak)
		a := c.assigns[ak]
		if th == teacher && a.status == StatusConfirmed && c.tasks[tid].Semester == semester {
			load += c.workload(a.hours, c.tasks[tid])
		}
	}
	incoming := c.credits[k]
	shortfall := rank.MinLoad - load
	if shortfall < 0 {
		shortfall = 0
	}
	excess := load - rank.MinLoad
	if excess < 0 {
		excess = 0
	}
	used := incoming
	if used > shortfall {
		used = shortfall
	}
	if cap := rank.MinLoad / 4; used > cap {
		used = cap
	}
	res := &SettlementResult{
		TeacherID:      teacher,
		Semester:       semester,
		Load:           load,
		MinLoad:        rank.MinLoad,
		Shortfall:      shortfall,
		Excess:         excess,
		CreditEarned:   excess / 2,
		CreditUsed:     used,
		Unmet:          shortfall - used,
		CreditIncoming: incoming,
	}
	if c.frozen[semester] == nil {
		c.frozen[semester] = map[string]struct{}{}
	}
	c.frozen[semester][teacher] = struct{}{}
	c.settled[k] = res
	c.credits[NextSemester(semester)+"\x00"+teacher] = res.CreditEarned
	delete(c.credits, k)
	m.commit(c)
	return res, nil
}

// snapshot 为可比较的完整状态（含守恒不变量）。
type naiveSnapshot struct {
	now     int64
	items   []AssignmentView
	loads   map[string]int
	allocs  map[string]int
	settled map[string]SettlementResult
	credits map[string]int
}

func (m *naiveModel) snapshot() naiveSnapshot {
	snap := naiveSnapshot{
		now:     m.now,
		loads:   map[string]int{},
		allocs:  map[string]int{},
		settled: map[string]SettlementResult{},
		credits: map[string]int{},
	}
	keys := append([]string(nil), m.sorted...)
	sort.Strings(keys)
	for _, k := range keys {
		th, tid := splitKey(k)
		a := m.assigns[k]
		snap.items = append(snap.items, AssignmentView{
			TeacherID: th,
			TaskID:    tid,
			Hours:     a.hours,
			Status:    a.status,
			Deadline:  a.deadline,
			WeekStart: a.weekStart,
			WeekEnd:   a.weekEnd,
		})
	}
	teachers := make([]string, 0, len(m.ranks))
	for t := range m.ranks {
		teachers = append(teachers, t)
	}
	sort.Strings(teachers)
	for _, t := range teachers {
		snap.loads[t] = m.loadOf(t)
	}
	tids := make([]string, 0, len(m.tasks))
	for t := range m.tasks {
		tids = append(tids, t)
	}
	sort.Strings(tids)
	for _, t := range tids {
		snap.allocs[t] = m.assignedHours(t)
	}
	for k, v := range m.settled {
		snap.settled[k] = *v
	}
	for k, v := range m.credits {
		snap.credits[k] = v
	}
	return snap
}
