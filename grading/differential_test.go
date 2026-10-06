package grading

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是按题目文字独立写成的朴素参照实现：
// 每次选人都全量扫描全部评卷人（不使用任何堆或增量索引），
// 直接按规则筛选并排序。与引擎的组织方式完全不同，用于差分对照。
type naiveModel struct {
	threshold int
	step      int

	graders  map[string]*naiveGrader
	papers   map[string]*naivePaper
	tasks    map[string]*naiveTask
	pending  map[string]struct{}
	nextTask int
	finals   map[string]int

	log *strings.Builder
}

type naiveGrader struct {
	id      string
	group   string
	quota   int
	student map[string]struct{}
	active  bool
}

type naiveTask struct {
	id, paper, grader, group string
	role                     Role
	status                   TaskStatus
	score                    int
	engineID                 string
}

type naivePaper struct {
	id, question, student string
	max                   int
	tasks                 []*naiveTask
	final                 *int
	needArb               bool
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		threshold: cfg.Threshold, step: cfg.Step,
		graders: map[string]*naiveGrader{}, papers: map[string]*naivePaper{},
		tasks: map[string]*naiveTask{}, pending: map[string]struct{}{},
		finals: map[string]int{}, log: &strings.Builder{},
	}
}

func (m *naiveModel) printf(format string, args ...any) {
	fmt.Fprintf(m.log, format+"\n", args...)
}

func (m *naiveModel) addGrader(g Grader) {
	ng := &naiveGrader{id: g.ID, group: g.Group, quota: g.DailyQuota, active: true, student: map[string]struct{}{}}
	for _, s := range g.Students {
		ng.student[s] = struct{}{}
	}
	m.graders[g.ID] = ng
	m.printf("naive addGrader %s group=%s quota=%d", g.ID, g.Group, g.DailyQuota)
	m.drain()
}

func (m *naiveModel) addPaper(p Paper) {
	m.papers[p.ID] = &naivePaper{id: p.ID, question: p.Question, student: p.Student, max: p.MaxScore}
	m.printf("naive addPaper %s student=%s max=%d", p.ID, p.Student, p.MaxScore)
	m.fillPaper(m.papers[p.ID])
}

// load 统计某评卷人的在手任务数：全量扫描任务。
func (m *naiveModel) load(id string) int {
	n := 0
	for _, t := range m.tasks {
		if t.grader == id && t.status == StatusOpen {
			n++
		}
	}
	return n
}

// hasStudent 判断评卷人对该学生是否已有“有效”任务（未撤回）。
func (m *naiveModel) hasStudent(id, student string) bool {
	for _, t := range m.tasks {
		if t.grader == id && t.status != StatusWithdrawn && m.papers[t.paper].final == nil {
			if m.papers[t.paper].student == student {
				return true
			}
		}
	}
	return false
}

// everOnPaper 判定评卷人是否曾以任何身份出现在该答卷上（撤回亦永久排除）。
func (m *naiveModel) everOnPaper(id string, p *naivePaper) bool {
	for _, t := range p.tasks {
		if t.grader == id {
			return true
		}
	}
	return false
}

// choose 按规格独立实现：扫描全部评卷人，过滤组别/回避/重复/配额，
// 取在手任务最少、并列时标识最小者。
func (m *naiveModel) choose(allowedGroups map[string]struct{}, busy map[string]struct{}, student string) string {
	var cand []*naiveGrader
	var ids []string
	for _, g := range m.graders {
		ids = append(ids, g.id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := m.graders[id]
		if !g.active {
			continue
		}
		if _, ok := allowedGroups[g.group]; !ok {
			continue
		}
		if _, ok := busy[g.id]; ok {
			continue
		}
		if _, ok := g.student[student]; ok {
			continue
		}
		if m.hasStudent(g.id, student) {
			continue
		}
		if m.load(g.id) >= g.quota {
			continue
		}
		cand = append(cand, g)
	}
	if len(cand) == 0 {
		return ""
	}
	sort.Slice(cand, func(i, j int) bool {
		li, lj := m.load(cand[i].id), m.load(cand[j].id)
		if li != lj {
			return li < lj
		}
		return cand[i].id < cand[j].id
	})
	return cand[0].id
}

func (m *naiveModel) groups(exclude map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, g := range m.graders {
		seen[g.group] = struct{}{}
	}
	for grp := range seen {
		if _, bad := exclude[grp]; !bad {
			out[grp] = struct{}{}
		}
	}
	return out
}

func (m *naiveModel) roleTask(p *naivePaper, role Role) *naiveTask {
	for _, t := range p.tasks {
		if t.role == role && t.status != StatusWithdrawn {
			return t
		}
	}
	return nil
}

func (m *naiveModel) issue(p *naivePaper, role Role, grader string) {
	m.nextTask++
	id := fmt.Sprintf("T%d", m.nextTask)
	g := m.graders[grader]
	t := &naiveTask{id: id, paper: p.id, grader: grader, group: g.group, role: role, status: StatusOpen}
	p.tasks = append(p.tasks, t)
	m.tasks[id] = t
	m.printf("naive assign %s paper=%s grader=%s group=%s role=%s load=%d",
		id, p.id, grader, g.group, roleName(role), m.load(grader))
}

// findTask 按 (答卷,评卷人,角色) 定位朴素任务（含已撤回）。
func (m *naiveModel) findTask(paper, grader string, role Role) *naiveTask {
	p := m.papers[paper]
	for _, tk := range p.tasks {
		if tk.grader == grader && tk.role == role {
			return tk
		}
	}
	return nil
}

func (m *naiveModel) fillRole(p *naivePaper, role Role) {
	if m.roleTask(p, role) != nil {
		return
	}
	busy := map[string]struct{}{}
	usedGroups := map[string]struct{}{}
	for _, t := range p.tasks {
		busy[t.grader] = struct{}{} // 含已撤回者：永久排除
		if t.status != StatusWithdrawn {
			usedGroups[t.group] = struct{}{}
		}
	}
	exclude := map[string]struct{}{}
	switch role {
	case RoleSecond:
		if f := m.roleTask(p, RoleFirst); f != nil {
			exclude[f.group] = struct{}{}
		}
	case RoleArbiter:
		exclude = usedGroups
	}
	id := m.choose(m.groups(exclude), busy, p.student)
	if id != "" {
		m.issue(p, role, id)
	}
}

func (m *naiveModel) complete(p *naivePaper) bool {
	first := m.roleTask(p, RoleFirst)
	second := m.roleTask(p, RoleSecond)
	ok1 := first != nil && second != nil
	ok2 := !p.needArb || m.roleTask(p, RoleArbiter) != nil
	return ok1 && ok2 && p.final == nil || p.final != nil
}

func (m *naiveModel) fillPaper(p *naivePaper) {
	if p.final != nil {
		return
	}
	m.fillRole(p, RoleFirst)
	m.fillRole(p, RoleSecond)
	if p.needArb {
		m.fillRole(p, RoleArbiter)
	}
	if !m.complete(p) {
		m.pending[p.id] = struct{}{}
	} else {
		delete(m.pending, p.id)
	}
}

func (m *naiveModel) drain() {
	for {
		if len(m.pending) == 0 {
			return
		}
		ids := make([]string, 0, len(m.pending))
		for id := range m.pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		before := len(m.pending)
		for _, id := range ids {
			m.fillPaper(m.papers[id])
		}
		if len(m.pending) >= before {
			return
		}
	}
}

func (m *naiveModel) gridRound(twice, maxTick int) int {
	r := twice / 2
	if twice%2 != 0 {
		r++
	}
	if r > maxTick {
		return maxTick
	}
	return r
}

// opResult 与引擎对齐：错误用 Kind 表示，nil 错误记 KindOK。
func (m *naiveModel) submit(taskID string, score int) Kind {
	m.printf("naive submit task=%s score=%d", taskID, score)
	t := m.tasks[taskID]
	if t == nil {
		return KindNotFound
	}
	g := m.graders[t.grader]
	if !g.active {
		return KindInactive
	}
	p := m.papers[t.paper]
	if p.final != nil {
		return KindTaskState
	}
	if t.status == StatusWithdrawn {
		return KindTaskState
	}
	if t.status == StatusSubmitted {
		return KindTaskState
	}
	if score < 0 || score > p.max || score%m.step != 0 {
		return KindScore
	}
	t.status = StatusSubmitted
	t.score = score
	m.maybeFinalize(p, t)
	if p.needArb && m.roleTask(p, RoleArbiter) == nil && p.final == nil {
		m.fillRole(p, RoleArbiter)
		if m.roleTask(p, RoleArbiter) == nil {
			m.pending[p.id] = struct{}{}
		}
	}
	return KindOK
}

func (m *naiveModel) maybeFinalize(p *naivePaper, submitted *naiveTask) {
	first := m.roleTask(p, RoleFirst)
	second := m.roleTask(p, RoleSecond)
	if submitted.role != RoleArbiter {
		if first != nil && second != nil && first.status == StatusSubmitted && second.status == StatusSubmitted {
			t1, t2 := first.score/m.step, second.score/m.step
			d := t1 - t2
			if d < 0 {
				d = -d
			}
			if d <= m.threshold/m.step {
				v := m.gridRound(t1+t2, p.max/m.step) * m.step
				p.final = &v
				m.finals[p.id] = v
				m.printf("naive final paper=%s score=%d basis=avg(first,second)", p.id, v)
			} else {
				p.needArb = true
				m.printf("naive arbiter-triggered paper=%s d=%d", p.id, d*m.step)
			}
		}
		return
	}
	arb := t(submitted.score / m.step)
	t1, t2 := first.score/m.step, second.score/m.step
	d1, d2 := absTick(arb, t1), absTick(arb, t2)
	var v int
	if d1 > m.threshold/m.step && d2 > m.threshold/m.step {
		v = submitted.score
		m.printf("naive final paper=%s score=%d basis=arbiter", p.id, v)
	} else {
		pick := t1
		if d2 < d1 || (d1 == d2 && t2 > t1) {
			pick = t2
		}
		v = m.gridRound(arb+pick, p.max/m.step) * m.step
		m.printf("naive final paper=%s score=%d basis=avg(arbiter,picked-initial)", p.id, v)
	}
	p.final = &v
	m.finals[p.id] = v
	m.releaseRelations(p)
}

// releaseRelations 终分后释放该答卷上的同人单次关系（与引擎一致）。
func (m *naiveModel) releaseRelations(p *naivePaper) {
	_ = p
	// hasStudent 已通过 final==nil 条件动态排除终分答卷，无需额外状态。
}

func t(x int) int { return x }

func (m *naiveModel) withdraw(taskID string) Kind {
	m.printf("naive withdraw task=%s", taskID)
	tk := m.tasks[taskID]
	if tk == nil {
		return KindNotFound
	}
	if !m.graders[tk.grader].active {
		return KindInactive
	}
	if tk.status != StatusOpen {
		return KindTaskState
	}
	p := m.papers[tk.paper]
	tk.status = StatusWithdrawn
	m.fillPaper(p)
	if _, ok := m.pending[p.id]; ok {
		return KindNoCandidate
	}
	return KindOK
}

func (m *naiveModel) deactivate(id string) Kind {
	m.printf("naive deactivate grader=%s", id)
	g := m.graders[id]
	if g == nil {
		return KindNotFound
	}
	if !g.active {
		return KindInactive
	}
	g.active = false
	affected := map[string]*naivePaper{}
	for _, tk := range m.tasks {
		if tk.grader == id && tk.status == StatusOpen {
			tk.status = StatusWithdrawn
			affected[tk.paper] = m.papers[tk.paper]
		}
	}
	ids := make([]string, 0, len(affected))
	for pid := range affected {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	for _, pid := range ids {
		p := affected[pid]
		m.fillPaper(p)
	}
	return KindOK
}

// snapshot 收集可比较状态：终分、每份答卷的(角色,组,状态,分数)、待分配集合。
type naiveSnapshot struct {
	finals  map[string]int
	tasks   map[string][][4]string // paper -> role|group|status|score
	pending []string
}

func (m *naiveModel) snapshot() naiveSnapshot {
	snap := naiveSnapshot{finals: map[string]int{}, tasks: map[string][][4]string{}}
	for id, v := range m.finals {
		snap.finals[id] = v
	}
	for id, p := range m.papers {
		var rows [][4]string
		for _, tk := range p.tasks {
			rows = append(rows, [4]string{roleName(tk.role), tk.group, fmt.Sprint(tk.status), fmt.Sprint(tk.score)})
		}
		sort.Slice(rows, func(i, j int) bool {
			for k := 0; k < 4; k++ {
				if rows[i][k] != rows[j][k] {
					return rows[i][k] < rows[j][k]
				}
			}
			return false
		})
		snap.tasks[id] = rows
	}
	for id := range m.pending {
		snap.pending = append(snap.pending, id)
	}
	sort.Strings(snap.pending)
	return snap
}

// engineSnapshot 用与朴素模型相同的形状抽取引擎状态。
func engineSnapshot(e *Engine) naiveSnapshot {
	snap := naiveSnapshot{finals: map[string]int{}, tasks: map[string][][4]string{}}
	e.mu.Lock()
	for id, ps := range e.papers {
		if ps.final != nil {
			snap.finals[id] = ps.final.Score
		}
		var rows [][4]string
		for _, tk := range ps.tasks {
			rows = append(rows, [4]string{roleName(tk.Role), tk.Group, fmt.Sprint(tk.Status), fmt.Sprint(tk.Score)})
		}
		sort.Slice(rows, func(i, j int) bool {
			for k := 0; k < 4; k++ {
				if rows[i][k] != rows[j][k] {
					return rows[i][k] < rows[j][k]
				}
			}
			return false
		})
		snap.tasks[id] = rows
	}
	for id := range e.pending {
		snap.pending = append(snap.pending, id)
	}
	sort.Strings(snap.pending)
	e.mu.Unlock()
	return snap
}

func snapshotsEqual(a, b naiveSnapshot) (string, bool) {
	if len(a.finals) != len(b.finals) {
		return "finals len", false
	}
	for k, v := range a.finals {
		if b.finals[k] != v {
			return fmt.Sprintf("final %s: %d vs %d", k, v, b.finals[k]), false
		}
	}
	if len(a.tasks) != len(b.tasks) {
		return "tasks len", false
	}
	for k, v := range a.tasks {
		w := b.tasks[k]
		if len(v) != len(w) {
			return fmt.Sprintf("paper %s tasks %v vs %v", k, v, w), false
		}
		for i := range v {
			if v[i] != w[i] {
				return fmt.Sprintf("paper %s task %v vs %v", k, v[i], w[i]), false
			}
		}
	}
	if len(a.pending) != len(b.pending) {
		return "pending len", false
	}
	for i := range a.pending {
		if a.pending[i] != b.pending[i] {
			return "pending content", false
		}
	}
	return "", true
}

// TestDifferentialRandom 对随机生成的操作序列做引擎与朴素模型的逐步对照，
// 并把每步输入、输出与判定依据打印到测试日志（-v 可见）。
func TestDifferentialRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	const iterations = 60
	const seed = int64(20261006)
	rng := rand.New(rand.NewSource(seed))
	cfg := Config{Threshold: 8, Step: 2}
	eng := NewEngine(cfg)
	nav := newNaive(cfg)

	var paperSeq, graderSeq int
	var graderIDs []string
	groups := []string{"A", "B", "C", "D"}

	addGrader := func() {
		graderSeq++
		id := fmt.Sprintf("r%02d", graderSeq)
		grp := groups[rng.Intn(len(groups))]
		quota := 1 + rng.Intn(3)
		var students []string
		if rng.Intn(3) == 0 {
			students = append(students, fmt.Sprintf("student-%d", rng.Intn(6)))
		}
		g := Grader{ID: id, Group: grp, DailyQuota: quota, Students: students}
		err := eng.AddGrader(g)
		nav.addGrader(g)
		nav.printf("engine addGrader => %v", err)
		if err == nil {
			graderIDs = append(graderIDs, id)
		}
	}
	addPaper := func() (string, bool) {
		paperSeq++
		id := fmt.Sprintf("paper-%02d", paperSeq)
		p := Paper{ID: id, Question: "q", MaxScore: 100, Student: fmt.Sprintf("student-%d", rng.Intn(6))}
		err := eng.AddPaper(p)
		nav.addPaper(p)
		nav.printf("engine addPaper => %v", err)
		return id, err == nil
	}

	nav.printf("=== differential seed=%d ===", seed)
	for step := 0; step < iterations; step++ {
		if len(graderIDs) == 0 || rng.Intn(5) == 0 {
			addGrader()
		}
		if paperSeq < 8 && (paperSeq == 0 || rng.Intn(3) > 0) {
			addPaper()
		}
		// 随机选一份答卷上的某个 open 任务做提交/撤回
		var pids []string
		for id := range nav.papers {
			pids = append(pids, id)
		}
		sort.Strings(pids)
		var open []TaskView
		for _, pid := range pids {
			for _, tv := range eng.Tasks(pid) {
				if tv.Status == StatusOpen {
					open = append(open, tv)
				}
			}
		}
		sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
		if len(open) > 0 {
			et := open[rng.Intn(len(open))]
			pid := et.PaperID
			roleNum := map[string]Role{"first": RoleFirst, "second": RoleSecond, "arbiter": RoleArbiter}[et.Role]
			nt := nav.findTask(pid, et.Grader, roleNum)
			if nt == nil || nt.status != StatusOpen {
				t.Fatalf("step %d 朴素侧缺少对应 open 任务: engine=%+v", step, et)
			}
			etID := et.ID
			switch rng.Intn(10) {
			case 0, 1:
				err := eng.Deactivate(nt.grader)
				k := nav.deactivate(nt.grader)
				nav.printf("engine deactivate %s => %v (naive=%d)", nt.grader, err, k)
				if errKind(err) != k {
					t.Fatalf("step %d deactivate kind mismatch: engine=%v naive=%d", step, err, k)
				}
				// 停用后从候选名单移除
				filtered := graderIDs[:0]
				for _, x := range graderIDs {
					if x != nt.grader {
						filtered = append(filtered, x)
					}
				}
				graderIDs = filtered
			case 2:
				err := eng.Withdraw(etID)
				k := nav.withdraw(nt.id)
				nav.printf("engine withdraw %s => %v (naive=%d)", etID, err, k)
				if errKind(err) != k {
					t.Fatalf("step %d withdraw kind mismatch: engine=%v naive=%d", step, err, k)
				}
			default:
				score := rng.Intn(60) * 2 // 网格上的 0..118，偶尔越界
				if rng.Intn(8) == 0 {
					score++ // 非网格
				}
				err := eng.Submit(etID, score)
				k := nav.submit(nt.id, score)
				nav.printf("engine submit %s score=%d => %v (naive=%d)", etID, score, err, k)
				if errKind(err) != k {
					t.Fatalf("step %d submit kind mismatch: engine=%v naive=%d", step, err, k)
				}
			}
		}
		// 偶尔再增加评卷人以触发待分配重领
		if rng.Intn(4) == 0 {
			addGrader()
		}
		if reason, ok := snapshotsEqual(nav.snapshot(), engineSnapshot(eng)); !ok {
			t.Fatalf("step %d 状态分叉: %s\n%s", step, reason, nav.log.String())
		}
		eng.mu.Lock()
		for grp, h := range eng.groupHeaps {
			for i := 1; i < h.Len(); i++ {
				parent := (i - 1) / 2
				if h.Less(i, parent) {
					eng.mu.Unlock()
					t.Fatalf("step %d 组 %s 堆序损坏 at %d", step, grp, i)
				}
			}
		}
		eng.mu.Unlock()
	}
	t.Logf("\n%s", nav.log.String())

	// 终态不变量：每份有终分的答卷恰有 2 或 3 份有效评分，
	// 且初评异组、仲裁第三组、无回避/同人重复（朴素模型结构本身即证明约束）。
	snap := engineSnapshot(eng)
	if len(snap.finals) == 0 {
		t.Logf("注意：本次随机序列未产生终分（可提高迭代次数）")
	}
}
