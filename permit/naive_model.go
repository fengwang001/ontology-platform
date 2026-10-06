package permit

// naiveStage 是朴素模型的环节状态（与 stageState 刻意不同构：显式逐日推进）。
type naiveStage struct {
	dept       string
	due        int
	auto       bool
	prereqs    []string
	status     StageStatus
	startDay   int
	passDay    int
	rejectDay  int
	remaining  int // 当前计时段剩余工作日（启动/恢复当日尚未扣）
	paused     bool
	deadline   int // 补正期限最后一日
	suppUsed   int
	overtime   bool
	countStart bool // 启动/恢复当日是否计入（首启动段 false，恢复段 true）
	firstCount int  // 当前计时段第一个应计数的工作日
}

// NaiveModel 是独立编写的参考实现：按日序号逐日 tick 的显式状态机。
// 它不引用 derive/segment 等代码，仅复用 Calendar 的工作日判断原语，
// 以保证与正式实现的算法路径相互独立。
type NaiveModel struct {
	cal         *Calendar
	suppWD      int
	suppLimit   int
	accepted    int
	stages      map[string]*naiveStage
	order       []string
	today       int
	withdrawn   bool
	withdrawDay int
	firstRej    int
}

// NewNaiveModel 在受理日构造朴素模型。
func NewNaiveModel(cal *Calendar, t *PermitType, accepted, suppWD, suppLimit int) *NaiveModel {
	m := &NaiveModel{
		cal:       cal,
		suppWD:    suppWD,
		suppLimit: suppLimit,
		accepted:  accepted,
		stages:    map[string]*naiveStage{},
		today:     accepted,
	}
	order, _ := topoOrder(t)
	m.order = order
	for i := range t.Stages {
		d := t.Stages[i]
		m.stages[d.ID] = &naiveStage{
			dept: d.Department, due: d.DueWorkdays, auto: d.AutoPass,
			prereqs: append([]string(nil), d.Prereqs...),
			status:  StageNotStarted, remaining: d.DueWorkdays,
		}
	}
	m.startRoots()
	return m
}

func (m *NaiveModel) startRoots() {
	for _, id := range m.order {
		st := m.stages[id]
		if len(st.prereqs) == 0 {
			st.status = StageActive
			st.startDay = m.accepted
		}
	}
}

func (m *NaiveModel) isWorking(d int) bool { return m.cal.IsWorking(d) }

// advanceTo 逐日推进到目标日（含），处理启动、计时、超时默认、补正逾期与封锁。
func (m *NaiveModel) advanceTo(target int) {
	for d := m.today + 1; d <= target; d++ {
		m.today = d
		m.tick(d)
	}
	m.evaluate()
}

func (m *NaiveModel) tick(d int) {
	if m.withdrawn {
		return
	}
	working := m.isWorking(d)

	// 启动/封锁可能在同一天级联（前置当日通过 -> 后继当日启动），先定点收敛启动。
	for {
		if !m.startStep(d) {
			break
		}
	}
	m.evaluate()

	// 计时：每个工作日恰好一次（启动当日不计；恢复当日计入）。
	if working {
		for _, id := range m.order {
			st := m.stages[id]
			if !m.counting(st, d) || d < st.firstCount {
				continue
			}
			st.remaining--
			if st.remaining < 0 {
				// 届满当日 remaining==0 仍有效；下一工作日变为 -1：
				// 自动通过环节即时通过；非自动环节钳制为 0 并置超时标记，不再下探。
				if st.auto {
					st.status = StageApproved
					st.passDay = d
					st.remaining = 0
				} else {
					st.overtime = true
					st.remaining = 0
				}
			}
		}
	}

	// 补正逾期：期限届满的下一工作日视为不通过。
	for _, id := range m.order {
		st := m.stages[id]
		if st.status == StagePaused && d > st.deadline && d == m.cal.NextWorkingAfter(st.deadline) {
			st.status = StageRejected
			st.rejectDay = d
			st.remaining = 0
		}
	}

	// 自动通过可能满足后继当日启动，再收敛一次（当日不再重复计时，因 d<firstCount 守卫）。
	m.startStep(d)
	m.evaluate()
}

// startStep 执行一轮“满足前置即启动 / 被拒后代封锁”，返回是否有变化。
// 启动按日序号发生，与当日是否工作日无关。
func (m *NaiveModel) startStep(d int) bool {
	anyChanged := false
	for {
		changed := false
		for _, id := range m.order {
			st := m.stages[id]
			if st.status != StageNotStarted {
				continue
			}
			for _, p := range st.prereqs {
				ps := m.stages[p]
				if ps.status == StageBlocked || ps.status == StageRejected {
					st.status = StageBlocked
					changed = true
					anyChanged = true
					break
				}
			}
			if st.status != StageNotStarted {
				continue
			}
			if m.prereqsPassed(st) {
				st.status = StageActive
				st.startDay = d
				st.countStart = false
				st.firstCount = m.cal.NextWorkingAfter(d)
				st.remaining = st.due
				changed = true
				anyChanged = true
			}
		}
		if !changed {
			return anyChanged
		}
	}
}

// counting 报告环节在日 d 是否处于计时（当天应消耗一个工作日额度）。
// 以“当前计时段第一个应计数的工作日”为界，天然跨越非工作日，
// 且与启动日是否工作日无关：首启动段起算工作日=启动日的下一工作日，
// 恢复段第一个计数工作日=恢复当日（若为工作日）否则次工作日。
func (m *NaiveModel) counting(st *naiveStage, d int) bool {
	if st.status != StageActive {
		return false
	}
	return d >= st.firstCount
}

func (m *NaiveModel) prereqsPassed(st *naiveStage) bool {
	for _, p := range st.prereqs {
		if m.stages[p].status != StageApproved {
			return false
		}
	}
	return true
}

// evaluate 计算首个不通过封锁线并标记永不启动的后继。
func (m *NaiveModel) evaluate() {
	first := 0
	for _, id := range m.order {
		st := m.stages[id]
		if st.status == StageRejected && (first == 0 || st.rejectDay < first) {
			first = st.rejectDay
		}
	}
	m.firstRej = first
	if first > 0 {
		for _, id := range m.order {
			st := m.stages[id]
			if st.status != StageNotStarted {
				continue
			}
			// 沿依赖图传播封锁（拓扑序保证前置先被标记）。
			for _, p := range st.prereqs {
				ps := m.stages[p]
				if ps.status == StageBlocked || ps.status == StageRejected {
					st.status = StageBlocked
					break
				}
			}
		}
	}
}

// ---- 显式操作（返回与 Service 相同类别的错误；被拒绝不改变状态）----

func (m *NaiveModel) decide(d int, stageID, dept string, dec Decision, suppLimit int) error {
	m.advanceTo(d)
	st, ok := m.stages[stageID]
	if !ok {
		return errf(ErrNotFound, "stage %q", stageID)
	}
	if st.status == StageApproved || st.status == StageRejected || st.status == StageWithdrawn {
		return errf(ErrStateNotAllowed, "status %d", st.status)
	}
	// 整件已终局（准予/撤回/不予且收尾结束）：状态不允许，先于无权限。
	if m.caseFinalForActive() {
		return errf(ErrStateNotAllowed, "final")
	}
	if st.dept != dept {
		return errf(ErrNoPermission, "dept")
	}
	if st.status == StageNotStarted || st.status == StageBlocked {
		return errf(ErrPrereqNotPassed, "stage %q", stageID)
	}
	if dec == DecideSupplement && st.status == StageActive && st.suppUsed >= suppLimit {
		return errf(ErrSupplementLimit, "limit")
	}
	switch dec {
	case DecideApprove:
		st.status = StageApproved
		st.passDay = d
		st.remaining = 0
	case DecideReject:
		st.status = StageRejected
		st.rejectDay = d
		st.remaining = 0
	case DecideSupplement:
		if st.status != StageActive {
			return errf(ErrStateNotAllowed, "not active")
		}
		st.status = StagePaused
		st.suppUsed++
		st.deadline = m.cal.NthWorkingOnOrAfter(d+1, m.suppWD)
	}
	// 手动办结当日可能满足后继启动条件：定点启动（当日不再重复计时）。
	for m.startStep(d) {
	}
	m.evaluate()
	return nil
}

func (m *NaiveModel) submitSupp(d int, stageID string) error {
	m.advanceTo(d)
	st, ok := m.stages[stageID]
	if !ok {
		return errf(ErrNotFound, "stage %q", stageID)
	}
	if st.status != StagePaused {
		return errf(ErrStateNotAllowed, "not paused")
	}
	if m.caseFinalForActive() {
		return errf(ErrStateNotAllowed, "final")
	}
	if d > st.deadline {
		return errf(ErrStateNotAllowed, "past deadline")
	}
	st.status = StageActive
	st.startDay = d
	st.countStart = true
	st.firstCount = m.cal.NextWorking(d)
	// 恢复当日若为工作日，当日即计入一个额度（含恢复当日）。
	if m.isWorking(d) {
		st.remaining--
		if st.remaining < 0 {
			if st.auto {
				st.status = StageApproved
				st.passDay = d
				st.remaining = 0
			} else {
				st.overtime = true
			}
		}
	}
	m.evaluate()
	return nil
}

func (m *NaiveModel) withdraw(d int) error {
	m.advanceTo(d)
	if m.caseOutcome() != OutcomePending {
		return errf(ErrStateNotAllowed, "outcome locked")
	}
	m.withdrawn = true
	m.withdrawDay = d
	for _, st := range m.stages {
		if st.status == StageActive || st.status == StagePaused || st.status == StageNotStarted {
			st.status = StageWithdrawn
		}
	}
	return nil
}

// outcomeLocked 报告结论是否已固定（撤回/准予/有不通过）。
func (m *NaiveModel) outcomeLocked() bool {
	if m.withdrawn {
		return true
	}
	allApp := true
	for _, st := range m.stages {
		if st.status == StageRejected {
			return true
		}
		if st.status != StageApproved {
			allApp = false
		}
	}
	return allApp
}

// View 返回朴素模型在当前推进日的某环节视图，字段口径与 StageView 对齐。
func (m *NaiveModel) View(stageID string) StageView {
	st := m.stages[stageID]
	started := st.status != StageNotStarted && st.status != StageBlocked
	remain := st.remaining
	if st.status == StageApproved || st.status == StageRejected {
		remain = 0
	}
	due := 0
	hasDue := st.status != StageNotStarted && st.status != StageBlocked
	if hasDue {
		// 朴素模型届满日：以正式日历按剩余额度反推当前段届满日（仅用于展示比对）。
		used := st.due - st.remaining
		if st.status == StageApproved || st.status == StageRejected {
			used = st.due
		}
		_ = used
	}
	return StageView{
		ID: stageID, Department: st.dept, Status: st.status,
		StartDay: st.startDay, Started: started,
		DueDay: due, HasDue: hasDue, Remaining: remain,
		Overtime: st.overtime, AutoPass: st.auto,
	}
}

// Status 返回环节状态。
func (m *NaiveModel) Status(stageID string) StageStatus { return m.stages[stageID].status }

// Outcome 朴素模型的整件结论。
func (m *NaiveModel) Outcome() CaseOutcome {
	if m.withdrawn {
		return OutcomeWithdrawn
	}
	if m.firstRej > 0 {
		return OutcomeDenied
	}
	all := true
	for _, st := range m.stages {
		if st.status != StageApproved {
			all = false
		}
	}
	if all {
		return OutcomeGranted
	}
	return OutcomePending
}

// FinalDay 终局时刻。
func (m *NaiveModel) FinalDay() (int, bool) {
	switch m.Outcome() {
	case OutcomeWithdrawn:
		return m.withdrawDay, true
	case OutcomeDenied:
		return m.firstRej, true
	case OutcomeGranted:
		last := 0
		for _, st := range m.stages {
			if st.passDay > last {
				last = st.passDay
			}
		}
		return last, true
	}
	return 0, false
}

// caseOutcome 朴素模型当前整件结论（与正式 derive 口径一致）。
func (m *NaiveModel) caseOutcome() CaseOutcome { return m.Outcome() }

// caseFinalForActive 报告对于“在办/暂停环节”的操作是否应被终局拒绝。
// 准予/撤回：拒绝；不予：仅当不存在任何在办或暂停环节（收尾已结束）才拒绝。
func (m *NaiveModel) caseFinalForActive() bool {
	if m.withdrawn {
		return true
	}
	allApp := true
	active := false
	for _, st := range m.stages {
		switch st.status {
		case StageRejected:
			allApp = false
		case StageActive, StagePaused:
			active = true
			allApp = false
		case StageApproved:
		default:
			allApp = false
		}
	}
	if allApp {
		return true
	}
	if m.firstRej > 0 {
		return !active
	}
	return false
}
