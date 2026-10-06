package grading

import (
	"sort"
	"sync"

	"container/heap"
)

// AuditEvent 记录一次成功的状态变更及其判定依据。被拒绝的操作不产生事件。
type AuditEvent struct {
	Seq    int            // 全局严格递增的发生序号
	Type   string         // assign/withdraw/submit/arbiter/final/grader_added/...
	Detail map[string]any // 涉及对象与关键数值
	Reason string         // 触发该事件的判定依据
}

// log 追加一条审计（调用方须持锁）。
func (e *Engine) log(typ string, detail map[string]any, reason string) {
	e.seq++
	e.audit = append(e.audit, AuditEvent{Seq: e.seq, Type: typ, Detail: detail, Reason: reason})
}

// Audit 按发生次序返回审计快照。
func (e *Engine) Audit() []AuditEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditEvent, len(e.audit))
	copy(out, e.audit)
	return out
}

// Config 是引擎静态配置。
// Step 为最小评分步长（网格间距）；Threshold 为分差阈值，恰等于阈值视为一致。
// 所有合法分数与阈值均须落在步长网格上。
type Config struct {
	Threshold int
	Step      int
}

// Engine 是阅卷分配与双评仲裁引擎。所有公开方法持有同一把锁，
// 因此并发调用的可观察结果等价于某个确定的串行顺序。
type Engine struct {
	mu         sync.Mutex
	cfg        Config
	graders    map[string]*graderState
	papers     map[string]*paperState
	groupHeaps map[string]*graderHeap
	pending    map[string]struct{}
	tasks      map[string]*Task
	audit      []AuditEvent
	seq        int
	nextTask   int
}

// NewEngine 创建引擎。
func NewEngine(cfg Config) *Engine {
	if cfg.Step <= 0 {
		cfg.Step = 1
	}
	if cfg.Threshold < 0 {
		cfg.Threshold = 0
	}
	return &Engine{
		cfg:        cfg,
		graders:    map[string]*graderState{},
		papers:     map[string]*paperState{},
		groupHeaps: map[string]*graderHeap{},
		pending:    map[string]struct{}{},
		tasks:      map[string]*Task{},
	}
}

// AddGrader 录入评卷人，并触发全部待分配任务的重新分配。
func (e *Engine) AddGrader(g Grader) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if g.ID == "" || g.Group == "" || g.DailyQuota <= 0 {
		return newError(KindInvalidParam, "评卷人标识、组别为空或配额非正: %+v", g)
	}
	if _, exists := e.graders[g.ID]; exists {
		return newError(KindInvalidParam, "评卷人已存在: %s", g.ID)
	}
	st := &graderState{
		desc:             g,
		active:           true,
		students:         map[string]struct{}{},
		assigned:         map[string]struct{}{},
		studentOpenCount: map[string]int{},
		heapIdx:          -1,
	}
	for _, s := range g.Students {
		st.students[s] = struct{}{}
	}
	e.graders[g.ID] = st
	h := e.groupHeaps[g.Group]
	if h == nil {
		h = &graderHeap{eng: e}
		e.groupHeaps[g.Group] = h
	}
	heap.Push(h, g.ID)
	e.log("grader_added", map[string]any{"grader": g.ID, "group": g.Group, "quota": g.DailyQuota}, "录入评卷人")
	e.drainPending()
	return nil
}

// AddPaper 录入答卷并自动选取两名初评人；无法配齐时该答卷进入待分配。
func (e *Engine) AddPaper(p Paper) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.ID == "" || p.Question == "" || p.Student == "" || p.MaxScore <= 0 {
		return newError(KindInvalidParam, "答卷字段非法: %+v", p)
	}
	if p.MaxScore%e.cfg.Step != 0 {
		return newError(KindInvalidParam, "满分不在步长网格上: max=%d step=%d", p.MaxScore, e.cfg.Step)
	}
	if _, exists := e.papers[p.ID]; exists {
		return newError(KindInvalidParam, "答卷已存在: %s", p.ID)
	}
	ps := &paperState{
		desc:   p,
		tasks:  map[string]*Task{},
		byRole: map[Role]*Task{},
	}
	e.papers[p.ID] = ps
	e.log("paper_added", map[string]any{"paper": p.ID, "question": p.Question, "max": p.MaxScore, "student": p.Student}, "录入答卷")
	// 无法立即配齐时答卷进入待分配，不视为录入失败；KindNoCandidate 归因
	// 出现在显式触发重分配（撤回/停用）场景。
	e.assignPaper(ps)
	return nil
}

// assignPaper 为答卷补齐所有当前应存在的初评任务；无法完成则标记待分配。
// 返回 true 表示该答卷当前需要的任务均已配齐。调用方须持锁。
func (e *Engine) assignPaper(ps *paperState) bool {
	if ps.final != nil {
		return true
	}
	e.fillRole(ps, RoleFirst)
	e.fillRole(ps, RoleSecond)
	if ps.byRole[RoleFirst] == nil || ps.byRole[RoleSecond] == nil {
		ps.unassigned = true
		e.pending[ps.desc.ID] = struct{}{}
	}
	if ps.needArb {
		e.fillRole(ps, RoleArbiter)
		if ps.byRole[RoleArbiter] == nil {
			ps.unassigned = true
			e.pending[ps.desc.ID] = struct{}{}
		}
	}
	if ps.byRole[RoleFirst] != nil && ps.byRole[RoleSecond] != nil &&
		(!ps.needArb || ps.byRole[RoleArbiter] != nil) {
		ps.unassigned = false
		delete(e.pending, ps.desc.ID)
		return true
	}
	return false
}

// fillRole 为某个角色选取评卷人并发放任务；已有有效任务时不动作。
func (e *Engine) fillRole(ps *paperState, role Role) {
	if t := ps.byRole[role]; t != nil {
		return
	}
	busy := map[string]struct{}{}
	var usedGroups []string
	for _, t := range ps.tasks {
		if t.Status != StatusWithdrawn {
			busy[t.Grader] = struct{}{}
			usedGroups = append(usedGroups, t.Group)
		} else {
			busy[t.Grader] = struct{}{}
		}
	}
	var allowed []string
	switch role {
	case RoleFirst:
		allowed = e.allGroups(nil)
	case RoleSecond:
		if t := ps.byRole[RoleFirst]; t != nil {
			allowed = e.allGroups(map[string]struct{}{t.Group: {}})
		} else {
			allowed = e.allGroups(nil)
		}
	case RoleArbiter:
		blocked := map[string]struct{}{}
		for _, g := range usedGroups {
			blocked[g] = struct{}{}
		}
		allowed = e.allGroups(blocked)
	}
	id, reason := e.selectCandidate(allowed, busy, ps.desc.Student, nil)
	if id == "" {
		return
	}
	e.issueTask(ps, role, id, reason)
}

// releaseStudent 在任务撤回/停用时按剩余有效任务数解除同人单次占用。
// 同一评卷人可能对同一学生持有多份不同答卷的有效任务，
// 只有全部解除后才可再次评阅该学生。调用方须持锁。
func (e *Engine) releaseStudent(g *graderState, student string) {
	if g.studentOpenCount[student] > 0 {
		return // 仍有其他未撤回任务面向该学生
	}
	delete(g.assigned, student)
}

// issueTask 生成任务、占用配额与在手计数、写入审计。调用方须持锁。
func (e *Engine) issueTask(ps *paperState, role Role, graderID string, reason string) {
	e.nextTask++
	tid := "T" + itoa(e.nextTask)
	g := e.graders[graderID]
	t := &Task{
		ID:      tid,
		PaperID: ps.desc.ID,
		Grader:  graderID,
		Group:   g.desc.Group,
		Role:    role,
		Status:  StatusOpen,
	}
	ps.tasks[tid] = t
	ps.byRole[role] = t
	e.tasks[tid] = t
	g.load++
	g.assigned[ps.desc.Student] = struct{}{}
	g.openTasks = append(g.openTasks, t)
	g.studentOpenCount[ps.desc.Student]++
	e.loadChanged(graderID)
	detail := map[string]any{
		"task": tid, "paper": ps.desc.ID, "grader": graderID,
		"group": g.desc.Group, "role": roleName(role),
	}
	e.log("assign", detail, reason)
}

// allGroups 返回排除 blocked 后的有序组别列表，保证选择与遍历次序确定。
func (e *Engine) allGroups(blocked map[string]struct{}) []string {
	out := make([]string, 0, len(e.groupHeaps))
	for g := range e.groupHeaps {
		if _, bad := blocked[g]; !bad {
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out
}

// drainPending 按答卷标识次序依次尝试补齐待分配答卷。
// 每个待分配答卷至多循环其角色数次，因此终止且确定。
func (e *Engine) drainPending() {
	for {
		if len(e.pending) == 0 {
			return
		}
		ids := make([]string, 0, len(e.pending))
		for id := range e.pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		before := len(e.pending)
		for _, id := range ids {
			e.assignPaper(e.papers[id])
		}
		if len(e.pending) >= before {
			return
		}
	}
}

func roleName(r Role) string {
	switch r {
	case RoleFirst:
		return "first"
	case RoleSecond:
		return "second"
	case RoleArbiter:
		return "arbiter"
	}
	return "?"
}
