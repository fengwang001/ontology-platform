package grading

import (
	"container/heap"
	"sort"
)

// FinalScoreView 是终分的对外只读视图。
type FinalScoreView struct {
	PaperID string
	Score   int
	Reason  string
}

// FinalScore 返回答卷终分；尚未产生时 ok 为 false。
func (e *Engine) FinalScore(paperID string) (FinalScoreView, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ps := e.papers[paperID]
	if ps == nil || ps.final == nil {
		return FinalScoreView{}, false
	}
	return FinalScoreView{PaperID: paperID, Score: ps.final.Score, Reason: ps.final.Reason}, true
}

// TaskView 是任务的对外只读视图。
type TaskView struct {
	ID      string
	PaperID string
	Grader  string
	Group   string
	Role    string
	Status  TaskStatus
	Score   int
}

// Tasks 返回某份答卷的全部任务（含已撤回），按任务标识排序。
func (e *Engine) Tasks(paperID string) []TaskView {
	e.mu.Lock()
	defer e.mu.Unlock()
	ps := e.papers[paperID]
	if ps == nil {
		return nil
	}
	var ids []string
	for id := range ps.tasks {
		ids = append(ids, id)
	}
	// 任务标识形如 T1/T2/…，字典序与数字序在测试规模内一致；这里按数值次序排序。
	sortTaskIDs(ids)
	out := make([]TaskView, 0, len(ids))
	for _, id := range ids {
		t := ps.tasks[id]
		out = append(out, TaskView{
			ID: t.ID, PaperID: t.PaperID, Grader: t.Grader, Group: t.Group,
			Role: roleName(t.Role), Status: t.Status, Score: t.Score,
		})
	}
	return out
}

// Pending 返回当前处于待分配状态的答卷标识（有序）。
func (e *Engine) Pending() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.pending))
	for id := range e.pending {
		out = append(out, id)
	}
	sortTaskIDs(out)
	return out
}

// Load 返回评卷人当前在手（待提交）任务数。不存在时第二返回值为 false。
func (e *Engine) Load(graderID string) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g := e.graders[graderID]
	if g == nil {
		return 0, false
	}
	return g.load, true
}

func sortTaskIDs(ids []string) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && taskNum(ids[j]) < taskNum(ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

func taskNum(id string) int {
	n := 0
	for i := 0; i < len(id); i++ {
		if id[i] >= '0' && id[i] <= '9' {
			n = n*10 + int(id[i]-'0')
		}
	}
	return n
}

// Submit 提交评分。分数为原始分值，必须是步长网格上的非负数且不超过满分。
// 错误优先级：参数非法 > 不存在 > 评卷人停用 > 任务状态 > 分数非法。
func (e *Engine) Submit(taskID string, score int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if taskID == "" {
		return newError(KindInvalidParam, "任务标识为空")
	}
	t := e.tasks[taskID]
	if t == nil {
		return newError(KindNotFound, "任务不存在: %s", taskID)
	}
	g := e.graders[t.Grader]
	if !g.active {
		return newError(KindInactive, "评卷人已停用: %s", t.Grader)
	}
	ps := e.papers[t.PaperID]
	if ps.final != nil {
		return newError(KindTaskState, "答卷已产生终分: %s", ps.desc.ID)
	}
	switch t.Status {
	case StatusWithdrawn:
		return newError(KindTaskState, "任务已撤回: %s", taskID)
	case StatusSubmitted:
		return newError(KindTaskState, "任务重复提交: %s", taskID)
	}
	step := e.cfg.Step
	maxTick := ps.desc.MaxScore / step
	if score < 0 || score > ps.desc.MaxScore || score%step != 0 {
		return newError(KindScore, "分数越界或不在步长网格上: score=%d max=%d step=%d", score, ps.desc.MaxScore, step)
	}
	tick := score / step

	// 全部前置校验通过后才改动状态，保证被拒绝操作零副作用。
	t.Status = StatusSubmitted
	t.Score = score
	g.load--
	g.openTasks = removeTask(g.openTasks, t)
	// 已提交评分仍属有效关系：终分产生前不得再评同一学生，故不释放 assigned。
	e.loadChanged(t.Grader)
	e.log("submit", map[string]any{
		"task": t.ID, "paper": ps.desc.ID, "grader": t.Grader,
		"role": roleName(t.Role), "score": score,
	}, "评分落在步长网格且任务处于待提交状态")

	if t.Role == RoleArbiter {
		e.finalizeArbitration(ps, tick, score)
		return nil
	}
	f := ps.byRole[RoleFirst]
	s := ps.byRole[RoleSecond]
	if f != nil && s != nil && f.Status == StatusSubmitted && s.Status == StatusSubmitted {
		t1, t2 := f.Score/step, s.Score/step
		diff := absTick(t1, t2)
		if diff <= e.cfg.Threshold/e.cfg.Step {
			finalTick := gridRound(t1+t2, maxTick)
			ps.final = &FinalScore{Score: finalTick * step, Reason: "初评分差不超过阈值，取平均并向满分靠拢"}
			e.releasePaperRelations(ps)
			e.log("final", map[string]any{
				"paper": ps.desc.ID, "final": ps.final.Score,
				"first": f.Score, "second": s.Score, "diff": diff * step,
			}, "diff<=threshold; 采用两份初评平均(ceil靠拢)")
		} else {
			ps.needArb = true
			e.log("arbiter", map[string]any{
				"paper": ps.desc.ID, "first": f.Score, "second": s.Score, "diff": diff * step,
			}, "diff>threshold，触发第三人仲裁；仲裁人须来自第三个评卷组")
			e.assignPaper(ps)
		}
	}
	return nil
}

// finalizeArbitration 按仲裁规则计算终分。
func (e *Engine) finalizeArbitration(ps *paperState, arbTick int, arbScore int) {
	f := ps.byRole[RoleFirst]
	s := ps.byRole[RoleSecond]
	t1, t2 := f.Score/e.cfg.Step, s.Score/e.cfg.Step
	maxTick := ps.desc.MaxScore / e.cfg.Step
	d1, d2 := absTick(arbTick, t1), absTick(arbTick, t2)
	var finalTick int
	var chosen string
	var reason string
	if d1 > e.cfg.Threshold/e.cfg.Step && d2 > e.cfg.Threshold/e.cfg.Step {
		finalTick = arbTick
		chosen = "arbiter"
		reason = "仲裁分与两份初评之差均超过阈值，终分取仲裁分本身"
	} else {
		pick := t1
		pickScore := f.Score
		chosen = "first"
		switch {
		case d1 < d2:
		case d2 < d1:
			pick, pickScore, chosen = t2, s.Score, "second"
		default: // 等距：与较高初评求平均
			if t2 > t1 {
				pick, pickScore, chosen = t2, s.Score, "second"
			}
		}
		finalTick = gridRound(arbTick+pick, maxTick)
		reason = "取差值较小的初评与仲裁分平均；等距时取较高初评；ceil靠拢"
		e.log("final", map[string]any{
			"paper": ps.desc.ID, "final": finalTick * e.cfg.Step,
			"arbiter": arbScore, "first": f.Score, "second": s.Score,
			"d1": d1 * e.cfg.Step, "d2": d2 * e.cfg.Step, "chosen_initial": chosen, "chosen_score": pickScore,
		}, reason)
		ps.final = &FinalScore{Score: finalTick * e.cfg.Step, Reason: reason}
		e.releasePaperRelations(ps)
		return
	}
	e.log("final", map[string]any{
		"paper": ps.desc.ID, "final": finalTick * e.cfg.Step,
		"arbiter": arbScore, "first": f.Score, "second": s.Score,
		"d1": d1 * e.cfg.Step, "d2": d2 * e.cfg.Step, "chosen_initial": chosen,
	}, reason)
	ps.final = &FinalScore{Score: finalTick * e.cfg.Step, Reason: reason}
	e.releasePaperRelations(ps)
}

// releasePaperRelations 在终分产生后释放该答卷占用的同人单次关系。
// 此时该答卷上的评分关系已结束，评卷人可评阅该学生的其他答卷。
func (e *Engine) releasePaperRelations(ps *paperState) {
	for _, t := range ps.tasks {
		if t.Status == StatusWithdrawn {
			continue
		}
		g := e.graders[t.Grader]
		g.studentOpenCount[ps.desc.Student]--
		e.releaseStudent(g, ps.desc.Student)
	}
}

// Withdraw 在提交评分前撤回任务；撤回后立即重新选取，原评卷人永久排除。
func (e *Engine) Withdraw(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if taskID == "" {
		return newError(KindInvalidParam, "任务标识为空")
	}
	t := e.tasks[taskID]
	if t == nil {
		return newError(KindNotFound, "任务不存在: %s", taskID)
	}
	g := e.graders[t.Grader]
	if !g.active {
		return newError(KindInactive, "评卷人已停用: %s", t.Grader)
	}
	if t.Status != StatusOpen {
		return newError(KindTaskState, "仅待提交任务可撤回: %s 状态=%d", taskID, t.Status)
	}
	ps := e.papers[t.PaperID]
	role := t.Role
	t.Status = StatusWithdrawn
	delete(ps.byRole, role)
	g.load--
	g.openTasks = removeTask(g.openTasks, t)
	g.studentOpenCount[ps.desc.Student]--
	e.releaseStudent(g, ps.desc.Student)
	e.loadChanged(t.Grader)
	e.log("withdraw", map[string]any{
		"task": t.ID, "paper": ps.desc.ID, "grader": t.Grader, "role": roleName(role),
	}, "提交前撤回；已完成的另一份初评保留；重分配永久排除该评卷人")
	if !e.assignPaper(ps) {
		return newError(KindNoCandidate, "撤回后答卷 %s 无合格重分配候选，已进入待分配", ps.desc.ID)
	}
	return nil
}

// Deactivate 整体停用评卷人：其全部待提交任务视同撤回并重新分配，
// 已提交评分保留；同组其他人不受影响。
func (e *Engine) Deactivate(graderID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if graderID == "" {
		return newError(KindInvalidParam, "评卷人标识为空")
	}
	g := e.graders[graderID]
	if g == nil {
		return newError(KindNotFound, "评卷人不存在: %s", graderID)
	}
	if !g.active {
		return newError(KindInactive, "评卷人已停用: %s", graderID)
	}
	g.active = false
	heap.Remove(e.groupHeaps[g.desc.Group], g.heapIdx)
	affected := map[string]*paperState{}
	open := make([]*Task, len(g.openTasks))
	copy(open, g.openTasks)
	for _, t := range open {
		ps := e.papers[t.PaperID]
		t.Status = StatusWithdrawn
		delete(ps.byRole, t.Role)
		g.studentOpenCount[ps.desc.Student]--
		e.releaseStudent(g, ps.desc.Student)
		affected[ps.desc.ID] = ps
		e.log("withdraw", map[string]any{
			"task": t.ID, "paper": ps.desc.ID, "grader": graderID, "role": roleName(t.Role),
		}, "评卷人被停用，待提交任务视同撤回；已提交评分保留")
	}
	g.load = 0
	g.openTasks = nil
	e.log("grader_deactivated", map[string]any{"grader": graderID, "group": g.desc.Group}, "整体停用；同组其他评卷人不受影响")
	affectedIDs := make([]string, 0, len(affected))
	for id := range affected {
		affectedIDs = append(affectedIDs, id)
	}
	sort.Strings(affectedIDs)
	for _, id := range affectedIDs {
		ps := affected[id]
		e.assignPaper(ps)
	}
	return nil
}

func removeTask(list []*Task, t *Task) []*Task {
	for i, x := range list {
		if x == t {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}
