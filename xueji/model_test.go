package xueji

// 本文件包含一个按规则独立写成的朴素模型（naive），用于与 Engine 对照：
// 模型不维护任何增量账目，状态、累计数、未结案判定全部以线性扫描和
// 逐学期重算的方式朴素导出；验证逻辑按固定优先级顺序直白展开。

import (
	"fmt"
	"math/rand"
	"testing"
)

type nStudent struct {
	enrollSem int
	versions  []Version
	apps      []*Application
}

type naive struct {
	clock    int64
	clockSet bool
	sems     []Semester
	cfg      Config
	majors   map[string]int
	extra    map[string]map[int]int
	used     map[string]map[int]int
	students map[string]*nStudent
	apps     map[string]*Application
	seq      int
}

func newNaive(sems []Semester, cfg Config) *naive {
	levels := make(map[AppType]int, 5)
	for t := AppSuspend; t <= AppWithdraw; t++ {
		n := cfg.ApprovalLevels[t]
		if n <= 0 {
			n = 1
		}
		levels[t] = n
	}
	cfg.ApprovalLevels = levels
	return &naive{
		sems:     sems,
		cfg:      cfg,
		majors:   make(map[string]int),
		extra:    make(map[string]map[int]int),
		used:     make(map[string]map[int]int),
		students: make(map[string]*nStudent),
		apps:     make(map[string]*Application),
	}
}

func (n *naive) semAt(t int64) int {
	for i, s := range n.sems {
		if t >= s.Start && t < s.End {
			return i
		}
	}
	return -1
}

func (n *naive) checkClock(now int64) *Error {
	if n.clockSet && now < n.clock {
		return errf(ErrClockRegression, "时刻 %d 早于当前时钟 %d", now, n.clock)
	}
	return nil
}

func (n *naive) stateAt(st *nStudent, t int64) (Version, bool) {
	for i := len(st.versions) - 1; i >= 0; i-- {
		if st.versions[i].EffectiveTick <= t {
			return st.versions[i], true
		}
	}
	return Version{}, false
}

// countStatus 朴素口径：逐学期数该学期起始时刻的状态。
func (n *naive) countStatus(st *nStudent, sem int, target State) int {
	c := 0
	for s := st.enrollSem; s <= sem; s++ {
		if v, ok := n.stateAt(st, n.sems[s].Start); ok && v.State == target {
			c++
		}
	}
	return c
}

func (n *naive) usedYears(st *nStudent, sem int) int {
	u := sem - st.enrollSem + 1 -
		n.countStatus(st, sem, StateSuspended) - n.countStatus(st, sem, StateRetained)
	if u < 0 {
		u = 0
	}
	return u
}

func (n *naive) pendingOf(st *nStudent) *Application {
	for _, a := range st.apps {
		if a.Status == AppPending {
			return a
		}
	}
	return nil
}

func (n *naive) quota(major string, sem int) int {
	return n.majors[major] + n.extra[major][sem] - n.used[major][sem]
}

func (n *naive) void(st *nStudent, a *Application, now int64) {
	a.Status = AppVoided
	a.CloseTick = now
}

// touch 与引擎一致的惰性落地规则，独立实现。
func (n *naive) touch(st *nStudent, now int64) {
	if p := n.pendingOf(st); p != nil && now-p.SubmitTick > n.cfg.TimeLimit {
		n.void(st, p, now)
	}
	sem := n.semAt(now)
	if sem < 0 || n.cfg.MaxStudySemesters <= 0 {
		return
	}
	last := st.versions[len(st.versions)-1]
	if last.State.Terminal() {
		return
	}
	if n.usedYears(st, sem) < n.cfg.MaxStudySemesters {
		return
	}
	eff := n.sems[sem].Start
	if eff <= last.EffectiveTick {
		eff = last.EffectiveTick + 1
	}
	vsem := sem
	if x := n.semAt(eff); x >= 0 {
		vsem = x
	}
	st.versions = append(st.versions, Version{State: StateWithdrawn, Major: last.Major, EffectiveTick: eff, Semester: vsem})
	if p := n.pendingOf(st); p != nil {
		n.void(st, p, now)
	}
}

func (n *naive) AddMajor(id string, quota int, now int64) error {
	if id == "" || quota < 0 {
		return errf(ErrInvalidParam, "专业参数非法")
	}
	if _, dup := n.majors[id]; dup {
		return errf(ErrInvalidParam, "专业已存在")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.majors[id] = quota
	n.clock, n.clockSet = now, true
	return nil
}

func (n *naive) AddStudent(id, major string, enrollSem int, now int64) error {
	if id == "" || enrollSem < 0 || enrollSem >= len(n.sems) {
		return errf(ErrInvalidParam, "入学参数非法")
	}
	if _, dup := n.students[id]; dup {
		return errf(ErrInvalidParam, "学生已存在")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.majors[major]; !ok {
		return errf(ErrNotFound, "专业不存在")
	}
	st := &nStudent{enrollSem: enrollSem}
	st.versions = append(st.versions, Version{StateEnrolled, major, n.sems[enrollSem].Start, enrollSem})
	n.students[id] = st
	n.clock, n.clockSet = now, true
	return nil
}

func (n *naive) AddQuota(major string, sem, delta int, now int64) error {
	if major == "" || sem < 0 || sem >= len(n.sems) || delta <= 0 {
		return errf(ErrInvalidParam, "名额参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.majors[major]; !ok {
		return errf(ErrNotFound, "专业不存在")
	}
	m := n.extra[major]
	if m == nil {
		m = make(map[int]int)
		n.extra[major] = m
	}
	m[sem] += delta
	n.clock, n.clockSet = now, true
	return nil
}

func (n *naive) Submit(studentID string, typ AppType, targetMajor, submitter string, now int64) (string, error) {
	if studentID == "" || submitter == "" || !typ.valid() {
		return "", errf(ErrInvalidParam, "申请参数非法")
	}
	if typ == AppTransfer && targetMajor == "" {
		return "", errf(ErrInvalidParam, "转专业缺少目标专业")
	}
	sem := n.semAt(now)
	if sem < 0 {
		return "", errf(ErrInvalidParam, "时刻不在任何学期内")
	}
	effSem := sem
	if now > n.sems[sem].Deadline {
		effSem = sem + 1
	}
	if effSem >= len(n.sems) {
		return "", errf(ErrInvalidParam, "不存在可生效的学期")
	}
	if err := n.checkClock(now); err != nil {
		return "", err
	}
	st, ok := n.students[studentID]
	if !ok {
		return "", errf(ErrNotFound, "学生不存在")
	}
	if typ == AppTransfer {
		if _, ok := n.majors[targetMajor]; !ok {
			return "", errf(ErrNotFound, "专业不存在")
		}
	}
	n.touch(st, now)
	last := st.versions[len(st.versions)-1]
	if last.State.Terminal() {
		return "", errf(ErrTerminal, "终态不可变更")
	}
	cur, ok := n.stateAt(st, now)
	if !ok || !allowedTransitions[cur.State][typ] {
		return "", errf(ErrStateNotAllowed, "状态不允许")
	}
	if n.pendingOf(st) != nil {
		return "", errf(ErrPendingExists, "已有未结案申请")
	}
	if typ == AppTransfer && now > n.sems[sem].Deadline {
		return "", errf(ErrDeadlinePassed, "转专业须在截止前提交")
	}
	switch typ {
	case AppSuspend:
		if n.countStatus(st, sem, StateSuspended)+1 > n.cfg.MaxSuspendSemesters {
			return "", errf(ErrCapExceeded, "累计休学超出上限")
		}
	case AppRetain:
		if n.countStatus(st, sem, StateRetained)+1 > n.cfg.MaxRetainSemesters {
			return "", errf(ErrCapExceeded, "累计保留学籍超出上限")
		}
	case AppResume:
		if n.cfg.MaxStudySemesters > 0 && n.usedYears(st, sem) >= n.cfg.MaxStudySemesters {
			return "", errf(ErrCapExceeded, "已用年限达到上限")
		}
	}
	n.seq++
	app := &Application{
		ID:                fmt.Sprintf("app-%d", n.seq),
		StudentID:         studentID,
		Type:              typ,
		TargetMajor:       targetMajor,
		Submitter:         submitter,
		SubmitTick:        now,
		Semester:          sem,
		EffectiveSemester: effSem,
		EffectiveTick:     n.sems[effSem].Start,
		LevelsNeeded:      n.cfg.ApprovalLevels[typ],
		Status:            AppPending,
	}
	st.apps = append(st.apps, app)
	n.apps[app.ID] = app
	n.clock, n.clockSet = now, true
	return app.ID, nil
}

func (n *naive) checkApprovable(appID, approver string, now int64) (*nStudent, *Application, error) {
	if appID == "" || approver == "" {
		return nil, nil, errf(ErrInvalidParam, "审批参数非法")
	}
	if err := n.checkClock(now); err != nil {
		return nil, nil, err
	}
	app, ok := n.apps[appID]
	if !ok {
		return nil, nil, errf(ErrNotFound, "申请不存在")
	}
	st := n.students[app.StudentID]
	n.touch(st, now)
	if last := st.versions[len(st.versions)-1]; last.State.Terminal() {
		return nil, nil, errf(ErrTerminal, "终态不可变更")
	}
	switch app.Status {
	case AppVoided:
		return nil, nil, errf(ErrDeadlinePassed, "申请已超时限作废")
	case AppApproved, AppRejected:
		return nil, nil, errf(ErrStateNotAllowed, "申请已结案")
	}
	if approver == app.Submitter {
		return nil, nil, errf(ErrNoPermission, "不得自审")
	}
	for _, a := range app.Approvers {
		if a == approver {
			return nil, nil, errf(ErrNoPermission, "审批人重复出现")
		}
	}
	return st, app, nil
}

func (n *naive) Approve(appID, approver string, now int64) error {
	st, app, err := n.checkApprovable(appID, approver, now)
	if err != nil {
		return err
	}
	if app.NextLevel == app.LevelsNeeded-1 {
		last := st.versions[len(st.versions)-1]
		if app.EffectiveTick <= last.EffectiveTick {
			app.Status = AppRejected
			app.CloseTick = now
			return errf(ErrEffectiveTooEarly, "生效时刻早于最新版本")
		}
		if app.Type == AppTransfer && n.quota(app.TargetMajor, app.EffectiveSemester) <= 0 {
			return errf(ErrQuotaInsufficient, "名额不足")
		}
	}
	app.Approvers = append(app.Approvers, approver)
	app.NextLevel++
	if app.NextLevel < app.LevelsNeeded {
		n.clock, n.clockSet = now, true
		return nil
	}
	app.Status = AppApproved
	app.CloseTick = now
	last := st.versions[len(st.versions)-1]
	v := Version{State: app.Type.targetState(), Major: last.Major,
		EffectiveTick: app.EffectiveTick, Semester: app.EffectiveSemester}
	if app.Type == AppTransfer {
		v.Major = app.TargetMajor
		m := n.used[app.TargetMajor]
		if m == nil {
			m = make(map[int]int)
			n.used[app.TargetMajor] = m
		}
		m[app.EffectiveSemester]++
	}
	st.versions = append(st.versions, v)
	n.clock, n.clockSet = now, true
	return nil
}

func (n *naive) Reject(appID, approver string, now int64) error {
	st, app, err := n.checkApprovable(appID, approver, now)
	if err != nil {
		return err
	}
	app.Status = AppRejected
	app.CloseTick = now
	_ = st
	n.clock, n.clockSet = now, true
	return nil
}

func (n *naive) QueryAt(studentID string, t int64) (Snapshot, error) {
	st, ok := n.students[studentID]
	if !ok {
		return Snapshot{}, errf(ErrNotFound, "学生不存在")
	}
	snap := Snapshot{}
	if v, ok := n.stateAt(st, t); ok {
		snap.Found, snap.State, snap.Major = true, v.State, v.Major
	}
	var last *Application
	for _, a := range st.apps {
		if a.SubmitTick <= t {
			last = a
		}
	}
	if last != nil {
		closed := last.Status != AppPending && t >= last.CloseTick
		expired := t-last.SubmitTick > n.cfg.TimeLimit
		snap.Pending = !closed && !expired
	}
	return snap, nil
}

func (n *naive) LedgerAt(studentID string, now int64) (int, int, int, error) {
	st, ok := n.students[studentID]
	if !ok {
		return 0, 0, 0, errf(ErrNotFound, "学生不存在")
	}
	sem := n.semAt(now)
	if sem < 0 {
		return 0, 0, 0, errf(ErrInvalidParam, "时刻不在任何学期内")
	}
	return n.countStatus(st, sem, StateSuspended),
		n.countStatus(st, sem, StateRetained),
		n.usedYears(st, sem), nil
}

// fuzzEnv 引擎与朴素模型的对照环境。
type fuzzEnv struct {
	t        *testing.T
	r        *rand.Rand
	e        *Engine
	n        *naive
	students []string
	majors   []string
	actors   []string
	appIDs   []string
	now      int64
	step     int
}

func (f *fuzzEnv) fatalf(format string, args ...any) {
	f.t.Fatalf("seed 对照失败 step=%d now=%d: %s", f.step, f.now, fmt.Sprintf(format, args...))
}

// checkBoth 比较两个错误是否一致（分类相同），并记录本步日志。
func (f *fuzzEnv) checkBoth(op string, errE, errN error) {
	ce, cn := CodeOf(errE), CodeOf(errN)
	if ce != cn {
		f.fatalf("%s: 引擎错误 %v, 模型错误 %v", op, errE, errN)
	}
	verdict := "接受（全部校验通过）"
	if errE != nil {
		verdict = fmt.Sprintf("拒绝（判定依据：%s）", ce)
	}
	f.t.Logf("step=%03d now=%d | %s | => %s", f.step, f.now, op, verdict)
}

// compareState 对照两实现的可观察状态：全部学生的时点查询与时长账目。
func (f *fuzzEnv) compareState() {
	for _, sid := range f.students {
		ts := []int64{f.now, f.r.Int63n(f.now + 1), int64(f.r.Intn(60)) * 1000}
		for _, at := range ts {
			se, ee := f.e.QueryAt(sid, at)
			sn, en := f.n.QueryAt(sid, at)
			if (ee == nil) != (en == nil) || se != sn {
				f.fatalf("QueryAt(%s,%d): 引擎 %+v/%v, 模型 %+v/%v", sid, at, se, ee, sn, en)
			}
		}
		if f.e.cal.At(f.now) >= 0 {
			s1, r1, u1, ee := f.e.LedgerAt(sid, f.now)
			s2, r2, u2, en := f.n.LedgerAt(sid, f.now)
			if (ee == nil) != (en == nil) || s1 != s2 || r1 != r2 || u1 != u2 {
				f.fatalf("LedgerAt(%s,%d): 引擎 %d/%d/%d/%v, 模型 %d/%d/%d/%v",
					sid, f.now, s1, r1, u1, ee, s2, r2, u2, en)
			}
		}
	}
	if err := f.e.Validate(f.now); err != nil {
		f.fatalf("引擎不变量被破坏: %v", err)
	}
}

func (f *fuzzEnv) pickApp() string {
	if len(f.appIDs) > 0 && f.r.Intn(5) > 0 {
		// 偏向近期申请，提高走完全部审批层级的概率
		lo := len(f.appIDs) - 20
		if lo < 0 {
			lo = 0
		}
		return f.appIDs[lo+f.r.Intn(len(f.appIDs)-lo)]
	}
	return fmt.Sprintf("app-%d", f.r.Intn(len(f.appIDs)+3)+1)
}

// TestFuzzAgainstNaiveModel 随机生成操作序列，逐步对照引擎与朴素模型，
// 日志打印每步输入、输出与判定依据。
func TestFuzzAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { fuzzRun(t, seed) })
	}
}

func fuzzRun(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	sems := make([]Semester, 60)
	for i := range sems {
		sems[i] = Semester{Start: int64(i * 1000), End: int64(i*1000 + 1000), Deadline: int64(i*1000 + 500)}
	}
	cal, err := NewCalendar(sems)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ApprovalLevels: map[AppType]int{
			AppSuspend: 1, AppResume: 1, AppTransfer: 1, AppRetain: 2, AppWithdraw: 1,
		},
		TimeLimit:           800,
		MaxSuspendSemesters: 1,
		MaxRetainSemesters:  1,
		MaxStudySemesters:   12,
	}
	e, err := NewEngine(cal, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &fuzzEnv{
		t: t, r: r, e: e, n: newNaive(sems, cfg),
		majors: []string{"cs", "math", "phy"},
		actors: []string{"adm", "u0", "u1"},
	}
	quotas := map[string]int{"cs": 1, "math": 0, "phy": 2}
	for _, m := range f.majors {
		if err := f.e.AddMajor(m, quotas[m], 0); err != nil {
			t.Fatal(err)
		}
		if err := f.n.AddMajor(m, quotas[m], 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("s%d", i)
		f.students = append(f.students, sid)
		m := f.majors[r.Intn(len(f.majors))]
		sem := r.Intn(2)
		if err := f.e.AddStudent(sid, m, sem, 0); err != nil {
			t.Fatal(err)
		}
		if err := f.n.AddStudent(sid, m, sem, 0); err != nil {
			t.Fatal(err)
		}
	}
	f.now = 10
	for f.step = 0; f.step < 400; f.step++ {
		f.now += r.Int63n(160)
		if r.Intn(25) == 0 { // 偶发的时钟回退场景
			f.now -= r.Int63n(300)
			if f.now < 0 {
				f.now = 0
			}
		}
		switch op := r.Intn(100); {
		case op < 38: // 提交申请
			sid := f.students[r.Intn(len(f.students))]
			typ := AppType(r.Intn(5))
			target := f.majors[r.Intn(len(f.majors))]
			actor := f.actors[r.Intn(len(f.actors))]
			if r.Intn(15) == 0 { // 偶发的非法参数场景
				sid, actor, target = "", "", ""
			}
			desc := fmt.Sprintf("Submit(%s,%s,target=%s,by=%s)", sid, typ, target, actor)
			idE, errE := f.e.Submit(sid, typ, target, actor, f.now)
			idN, errN := f.n.Submit(sid, typ, target, actor, f.now)
			f.checkBoth(desc, errE, errN)
			if errE == nil {
				if idE != idN {
					f.fatalf("%s: 申请号不一致 %s != %s", desc, idE, idN)
				}
				f.appIDs = append(f.appIDs, idE)
			}
		case op < 68: // 审批通过
			appID, actor := f.pickApp(), f.actors[r.Intn(len(f.actors))]
			desc := fmt.Sprintf("Approve(%s,by=%s)", appID, actor)
			f.checkBoth(desc, f.e.Approve(appID, actor, f.now), f.n.Approve(appID, actor, f.now))
		case op < 78: // 驳回
			appID, actor := f.pickApp(), f.actors[r.Intn(len(f.actors))]
			desc := fmt.Sprintf("Reject(%s,by=%s)", appID, actor)
			f.checkBoth(desc, f.e.Reject(appID, actor, f.now), f.n.Reject(appID, actor, f.now))
		case op < 86: // 管理追加名额
			m, sem, d := f.majors[r.Intn(len(f.majors))], r.Intn(60), 1+r.Intn(2)
			desc := fmt.Sprintf("AddQuota(%s,sem=%d,+%d)", m, sem, d)
			f.checkBoth(desc, f.e.AddQuota(m, sem, d, f.now), f.n.AddQuota(m, sem, d, f.now))
		default: // 纯查询步（不推进任何状态）
			f.t.Logf("step=%03d now=%d | QueryOnly | => 仅对照只读查询", f.step, f.now)
		}
		f.compareState()
	}
}

// benchEngine 构建含 n 名学生的引擎，目标学生带有若干历史版本，
// 用于证明时点查询与累计数判定的开销与其他学生总数无关。
func benchEngine(b *testing.B, n int) *Engine {
	b.Helper()
	sems := make([]Semester, 40)
	for i := range sems {
		sems[i] = Semester{Start: int64(i * 1000), End: int64(i*1000 + 1000), Deadline: int64(i*1000 + 500)}
	}
	cal, err := NewCalendar(sems)
	if err != nil {
		b.Fatal(err)
	}
	e, err := NewEngine(cal, Config{
		ApprovalLevels:      map[AppType]int{AppSuspend: 1, AppResume: 1},
		TimeLimit:           800,
		MaxSuspendSemesters: 100,
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := e.AddMajor("cs", 1, 0); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := e.AddStudent(fmt.Sprintf("stu-%d", i), "cs", 0, 0); err != nil {
			b.Fatal(err)
		}
	}
	// 目标学生制造 8 段休学/复学循环，形成 17 个版本
	for k := 1; k <= 8; k++ {
		base := int64(k*2) * 1000
		id, err := e.Submit("stu-0", AppSuspend, "", "adm", base+100)
		if err != nil {
			b.Fatal(err)
		}
		if err := e.Approve(id, "u0", base+101); err != nil {
			b.Fatal(err)
		}
		id, err = e.Submit("stu-0", AppResume, "", "adm", base+1100)
		if err != nil {
			b.Fatal(err)
		}
		if err := e.Approve(id, "u0", base+1101); err != nil {
			b.Fatal(err)
		}
	}
	return e
}

// BenchmarkQueryAtScale 学生总数 1k vs 100k 时点查询开销应基本持平。
func BenchmarkQueryAtScale(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("students=%d", n), func(b *testing.B) {
			e := benchEngine(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := e.QueryAt("stu-0", 15500); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLedgerAtScale 学生总数 1k vs 100k 累计数判定开销应基本持平。
func BenchmarkLedgerAtScale(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("students=%d", n), func(b *testing.B) {
			e := benchEngine(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, _, err := e.LedgerAt("stu-0", 15500); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
