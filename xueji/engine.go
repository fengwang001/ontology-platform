package xueji

import (
	"fmt"
	"sync"
)

// student 学生内部记录：版本链、申请历史、未结案申请指针。
// 全部为学生级数据，任何操作的开销都与其他学生总数无关。
type student struct {
	id        string
	enrollSem int
	versions  []Version // 按生效时刻严格递增
	apps      []*Application
	pending   *Application // 至多一个
}

// Engine 学籍异动状态机引擎。所有公开方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行顺序。
type Engine struct {
	mu         sync.Mutex
	cal        *Calendar
	cfg        Config
	clock      int64
	clockSet   bool
	students   map[string]*student
	majors     map[string]int         // 专业 -> 每学期默认接收名额
	quotaExtra map[string]map[int]int // 专业 -> 学期 -> 管理追加名额
	quotaUsed  map[string]map[int]int // 专业 -> 学期 -> 已扣减名额
	apps       map[string]*Application
	seq        int
	audit      []AuditEvent
}

// NewEngine 构建引擎；审批层级缺省按 1 层处理。
func NewEngine(cal *Calendar, cfg Config) (*Engine, error) {
	if cal == nil {
		return nil, errf(ErrInvalidParam, "缺少学期日历")
	}
	if cfg.TimeLimit < 0 || cfg.MaxSuspendSemesters < 0 || cfg.MaxRetainSemesters < 0 || cfg.MaxStudySemesters < 0 {
		return nil, errf(ErrInvalidParam, "配置上限不得为负")
	}
	levels := make(map[AppType]int, 5)
	for t := AppSuspend; t <= AppWithdraw; t++ {
		n := cfg.ApprovalLevels[t]
		if n <= 0 {
			n = 1
		}
		levels[t] = n
	}
	cfg.ApprovalLevels = levels
	return &Engine{
		cal:        cal,
		cfg:        cfg,
		students:   make(map[string]*student),
		majors:     make(map[string]int),
		quotaExtra: make(map[string]map[int]int),
		quotaUsed:  make(map[string]map[int]int),
		apps:       make(map[string]*Application),
	}, nil
}

// checkClock 时钟回退校验。时钟仅在被接受的操作上推进，被拒绝的操作不推进时钟。
func (e *Engine) checkClock(now int64) *Error {
	if e.clockSet && now < e.clock {
		return errf(ErrClockRegression, "时刻 %d 早于当前时钟 %d", now, e.clock)
	}
	return nil
}

func (e *Engine) advance(now int64) {
	e.clock = now
	e.clockSet = true
}

func (e *Engine) log(tick int64, kind, sid, appID, detail string) {
	e.audit = append(e.audit, AuditEvent{Tick: tick, Kind: kind, StudentID: sid, AppID: appID, Detail: detail})
}

// lastVersion 返回生效时刻最新的版本。
func (e *Engine) lastVersion(st *student) Version {
	return st.versions[len(st.versions)-1]
}

// versionAt 返回 t 时刻生效的版本（最后一个生效时刻 <= t 的版本）。
func versionAt(st *student, t int64) (Version, bool) {
	vs := st.versions
	lo, hi := 0, len(vs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if vs[mid].EffectiveTick <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Version{}, false
	}
	return vs[lo-1], true
}

// countStatus 统计截至学期 sem（含）状态为 target 的学期数。
// 只遍历该学生自身版本链，开销与其他学生无关。
func countStatus(st *student, sem int, target State) int {
	total := 0
	vs := st.versions
	for i, v := range vs {
		if v.Semester > sem {
			break
		}
		if v.State != target {
			continue
		}
		end := sem + 1
		if i+1 < len(vs) && vs[i+1].Semester < end {
			end = vs[i+1].Semester
		}
		if d := end - v.Semester; d > 0 {
			total += d
		}
	}
	return total
}

// suspCum 累计已生效休学学期数（含当前学期，若当前处于休学）。
func (e *Engine) suspCum(st *student, sem int) int {
	return countStatus(st, sem, StateSuspended)
}

// retCum 累计已生效保留学籍学期数。
func (e *Engine) retCum(st *student, sem int) int {
	return countStatus(st, sem, StateRetained)
}

// usedYears 已用学业年限（学期计）：自入学学期起，扣除休学与保留学籍学期。
func (e *Engine) usedYears(st *student, sem int) int {
	used := sem - st.enrollSem + 1 - e.suspCum(st, sem) - e.retCum(st, sem)
	if used < 0 {
		used = 0
	}
	return used
}

// remainingQuota 目标专业在指定学期的剩余接收名额。
func (e *Engine) remainingQuota(major string, sem int) int {
	return e.majors[major] + e.quotaExtra[major][sem] - e.quotaUsed[major][sem]
}

func (e *Engine) useQuota(major string, sem int) {
	m := e.quotaUsed[major]
	if m == nil {
		m = make(map[int]int)
		e.quotaUsed[major] = m
	}
	m[sem]++
}

// voidApp 作废申请（系统惰性落地事件，入审计）。
func (e *Engine) voidApp(st *student, app *Application, now int64, reason string) {
	app.Status = AppVoided
	app.CloseTick = now
	if st.pending == app {
		st.pending = nil
	}
	e.log(now, "void", st.id, app.ID, reason)
}

// touch 触及学生时的惰性落地：先落地超时作废，再落地年限用尽退学。
// 这些是规则已确定的事实，只是延迟到下一次触及该学生时物化，入审计。
func (e *Engine) touch(st *student, now int64) {
	if p := st.pending; p != nil && now-p.SubmitTick > e.cfg.TimeLimit {
		e.voidApp(st, p, now, "超过审批时限自动作废")
	}
	sem := e.cal.At(now)
	if sem < 0 || e.cfg.MaxStudySemesters <= 0 {
		return
	}
	last := e.lastVersion(st)
	if last.State.Terminal() {
		return
	}
	if e.usedYears(st, sem) < e.cfg.MaxStudySemesters {
		return
	}
	eff := e.cal.Start(sem)
	if eff <= last.EffectiveTick {
		eff = last.EffectiveTick + 1 // 保持版本链按生效时刻严格递增
	}
	vsem := sem
	if x := e.cal.At(eff); x >= 0 {
		vsem = x
	}
	st.versions = append(st.versions, Version{State: StateWithdrawn, Major: last.Major, EffectiveTick: eff, Semester: vsem})
	if st.pending != nil {
		e.voidApp(st, st.pending, now, "年限用尽退学，未结案申请随之作废")
	}
	e.log(now, "lazy-withdraw", st.id, "", fmt.Sprintf("已用年限达到上限 %d，惰性落地为退学", e.cfg.MaxStudySemesters))
}

// AddMajor 注册专业及其每学期默认接收名额。
func (e *Engine) AddMajor(id string, quotaPerSemester int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || quotaPerSemester < 0 {
		return errf(ErrInvalidParam, "专业参数非法")
	}
	if _, dup := e.majors[id]; dup {
		return errf(ErrInvalidParam, "专业 %s 已存在", id)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	e.majors[id] = quotaPerSemester
	e.log(now, "major", "", "", fmt.Sprintf("注册专业 %s 名额 %d", id, quotaPerSemester))
	e.advance(now)
	return nil
}

// AddStudent 学生入学：建立初始版本（在读，生效于入学学期起始时刻）。
func (e *Engine) AddStudent(id, major string, enrollSem int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || enrollSem < 0 || enrollSem >= e.cal.Count() {
		return errf(ErrInvalidParam, "入学参数非法")
	}
	if _, dup := e.students[id]; dup {
		return errf(ErrInvalidParam, "学生 %s 已存在", id)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if _, ok := e.majors[major]; !ok {
		return errf(ErrNotFound, "专业 %s 不存在", major)
	}
	st := &student{id: id, enrollSem: enrollSem}
	st.versions = append(st.versions, Version{
		State:         StateEnrolled,
		Major:         major,
		EffectiveTick: e.cal.Start(enrollSem),
		Semester:      enrollSem,
	})
	e.students[id] = st
	e.log(now, "enroll", id, "", fmt.Sprintf("入学专业 %s 学期 %d", major, enrollSem))
	e.advance(now)
	return nil
}

// AddQuota 管理操作：为指定专业的指定学期追加接收名额。
func (e *Engine) AddQuota(major string, sem, delta int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if major == "" || sem < 0 || sem >= e.cal.Count() || delta <= 0 {
		return errf(ErrInvalidParam, "名额参数非法")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if _, ok := e.majors[major]; !ok {
		return errf(ErrNotFound, "专业 %s 不存在", major)
	}
	m := e.quotaExtra[major]
	if m == nil {
		m = make(map[int]int)
		e.quotaExtra[major] = m
	}
	m[sem] += delta
	e.log(now, "quota", "", "", fmt.Sprintf("专业 %s 学期 %d 名额 +%d", major, sem, delta))
	e.advance(now)
	return nil
}

// Submit 提交异动申请。校验顺序即固定错误优先级：
// 参数非法 > 时钟回退 > 不存在 > 终态 > 状态不允许 > 已有未结案 >
// 截止已过 > 累计上限。生效学期在提交时确定：提交时刻 <= 截止时刻
// 取当前学期，否则取下一学期；生效时刻为对应学期起始时刻。
func (e *Engine) Submit(studentID string, typ AppType, targetMajor, submitter string, now int64) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 1. 参数非法
	if studentID == "" || submitter == "" || !typ.valid() {
		return "", errf(ErrInvalidParam, "申请参数非法")
	}
	if typ == AppTransfer && targetMajor == "" {
		return "", errf(ErrInvalidParam, "转专业缺少目标专业")
	}
	sem := e.cal.At(now)
	if sem < 0 {
		return "", errf(ErrInvalidParam, "时刻 %d 不在任何学期内", now)
	}
	effSem := sem
	if now > e.cal.Deadline(sem) {
		effSem = sem + 1
	}
	if effSem >= e.cal.Count() {
		return "", errf(ErrInvalidParam, "不存在可生效的学期")
	}
	// 2. 时钟回退
	if err := e.checkClock(now); err != nil {
		return "", err
	}
	// 3. 学生或专业不存在
	st, ok := e.students[studentID]
	if !ok {
		return "", errf(ErrNotFound, "学生 %s 不存在", studentID)
	}
	if typ == AppTransfer {
		if _, ok := e.majors[targetMajor]; !ok {
			return "", errf(ErrNotFound, "专业 %s 不存在", targetMajor)
		}
	}
	// 惰性落地：超时作废、年限用尽退学
	e.touch(st, now)
	// 4. 终态不可变更
	last := e.lastVersion(st)
	if last.State.Terminal() {
		return "", errf(ErrTerminal, "学生 %s 已处于终态 %s", studentID, last.State)
	}
	// 5. 状态不允许
	cur, ok := versionAt(st, now)
	if !ok {
		return "", errf(ErrStateNotAllowed, "学生 %s 尚无生效学籍版本", studentID)
	}
	if !allowedTransitions[cur.State][typ] {
		return "", errf(ErrStateNotAllowed, "状态 %s 不允许申请 %s", cur.State, typ)
	}
	// 6. 已有未结案申请
	if st.pending != nil {
		return "", errf(ErrPendingExists, "学生 %s 存在未结案申请 %s", studentID, st.pending.ID)
	}
	// 7. 转专业截止已过
	if typ == AppTransfer && now > e.cal.Deadline(sem) {
		return "", errf(ErrDeadlinePassed, "转专业须在学期截止前提交")
	}
	// 9. 累计上限
	switch typ {
	case AppSuspend:
		if e.suspCum(st, sem)+1 > e.cfg.MaxSuspendSemesters {
			return "", errf(ErrCapExceeded, "累计休学将达到 %d 学期，超出上限 %d",
				e.suspCum(st, sem)+1, e.cfg.MaxSuspendSemesters)
		}
	case AppRetain:
		if e.retCum(st, sem)+1 > e.cfg.MaxRetainSemesters {
			return "", errf(ErrCapExceeded, "累计保留学籍将达到 %d 学期，超出上限 %d",
				e.retCum(st, sem)+1, e.cfg.MaxRetainSemesters)
		}
	case AppResume:
		if e.cfg.MaxStudySemesters > 0 && e.usedYears(st, sem) >= e.cfg.MaxStudySemesters {
			return "", errf(ErrCapExceeded, "已用年限 %d 达到上限 %d，复学拒绝",
				e.usedYears(st, sem), e.cfg.MaxStudySemesters)
		}
	}
	// 通过：建立申请
	e.seq++
	app := &Application{
		ID:                fmt.Sprintf("app-%d", e.seq),
		StudentID:         studentID,
		Type:              typ,
		TargetMajor:       targetMajor,
		Submitter:         submitter,
		SubmitTick:        now,
		Semester:          sem,
		EffectiveSemester: effSem,
		EffectiveTick:     e.cal.Start(effSem),
		LevelsNeeded:      e.cfg.ApprovalLevels[typ],
		Status:            AppPending,
	}
	st.pending = app
	st.apps = append(st.apps, app)
	e.apps[app.ID] = app
	e.log(now, "submit", studentID, app.ID,
		fmt.Sprintf("申请 %s 提交于学期 %d，生效学期 %d", typ, sem, effSem))
	e.advance(now)
	return app.ID, nil
}

// checkApprovable 审批/驳回的公共前置校验，返回申请所属学生。
// 顺序：参数非法 > 时钟回退 > 不存在 > （惰性落地）> 终态 > 申请已结案/已作废 > 无权限。
func (e *Engine) checkApprovable(appID, approver string, now int64) (*student, *Application, error) {
	if appID == "" || approver == "" {
		return nil, nil, errf(ErrInvalidParam, "审批参数非法")
	}
	if err := e.checkClock(now); err != nil {
		return nil, nil, err
	}
	app, ok := e.apps[appID]
	if !ok {
		return nil, nil, errf(ErrNotFound, "申请 %s 不存在", appID)
	}
	st := e.students[app.StudentID]
	e.touch(st, now)
	if last := e.lastVersion(st); last.State.Terminal() {
		return nil, nil, errf(ErrTerminal, "学生 %s 已处于终态 %s", st.id, last.State)
	}
	switch app.Status {
	case AppVoided:
		return nil, nil, errf(ErrDeadlinePassed, "申请 %s 已超时限作废", appID)
	case AppApproved, AppRejected:
		return nil, nil, errf(ErrStateNotAllowed, "申请 %s 已结案（%s）", appID, app.Status)
	}
	if approver == app.Submitter {
		return nil, nil, errf(ErrNoPermission, "审批人 %s 不得审批本人提交的申请", approver)
	}
	for _, a := range app.Approvers {
		if a == approver {
			return nil, nil, errf(ErrNoPermission, "审批人 %s 不得在同一申请的多个层级重复出现", approver)
		}
	}
	return st, app, nil
}

// Approve 审批通过当前层级；最终层级通过时落地状态版本。
// 最终层级的附加校验（优先级靠后）：生效时刻早于最新版本 > 名额不足。
func (e *Engine) Approve(appID, approver string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, app, err := e.checkApprovable(appID, approver, now)
	if err != nil {
		return err
	}
	final := app.NextLevel == app.LevelsNeeded-1
	if final {
		last := e.lastVersion(st)
		// 生效时刻不得早于（含等于）当前最新版本的生效时刻
		if app.EffectiveTick <= last.EffectiveTick {
			app.Status = AppRejected
			app.CloseTick = now
			if st.pending == app {
				st.pending = nil
			}
			e.log(now, "reject", st.id, app.ID,
				fmt.Sprintf("生效时刻 %d 不晚于最新版本 %d，结案驳回", app.EffectiveTick, last.EffectiveTick))
			return errf(ErrEffectiveTooEarly, "生效时刻 %d 早于最新版本 %d", app.EffectiveTick, last.EffectiveTick)
		}
		if app.Type == AppTransfer && e.remainingQuota(app.TargetMajor, app.EffectiveSemester) <= 0 {
			// 名额不足：申请保持待审，该层级不落任何记录，可追加名额后重试
			return errf(ErrQuotaInsufficient, "专业 %s 学期 %d 名额不足", app.TargetMajor, app.EffectiveSemester)
		}
	}
	app.Approvers = append(app.Approvers, approver)
	app.NextLevel++
	if app.NextLevel < app.LevelsNeeded {
		e.log(now, "level-approve", st.id, app.ID,
			fmt.Sprintf("层级 %d/%d 由 %s 通过", app.NextLevel, app.LevelsNeeded, approver))
		e.advance(now)
		return nil
	}
	// 最终层级通过：落地版本
	app.Status = AppApproved
	app.CloseTick = now
	st.pending = nil
	last := e.lastVersion(st)
	v := Version{
		State:         app.Type.targetState(),
		Major:         last.Major,
		EffectiveTick: app.EffectiveTick,
		Semester:      app.EffectiveSemester,
	}
	if app.Type == AppTransfer {
		v.Major = app.TargetMajor // 转专业在读状态不变，仅更换专业归属
		e.useQuota(app.TargetMajor, app.EffectiveSemester)
	}
	st.versions = append(st.versions, v)
	e.log(now, "approve", st.id, app.ID,
		fmt.Sprintf("申请 %s 终审通过，%s 生效于时刻 %d", app.Type, v.State, v.EffectiveTick))
	e.advance(now)
	return nil
}

// Reject 驳回：任一层级驳回即结案。
func (e *Engine) Reject(appID, approver string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, app, err := e.checkApprovable(appID, approver, now)
	if err != nil {
		return err
	}
	app.Status = AppRejected
	app.CloseTick = now
	st.pending = nil
	e.log(now, "reject", st.id, app.ID, fmt.Sprintf("层级 %d 由 %s 驳回", app.NextLevel, approver))
	e.advance(now)
	return nil
}
