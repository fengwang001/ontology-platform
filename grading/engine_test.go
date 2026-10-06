package grading

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func errKind(err error) Kind {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Kind
	}
	return KindOK
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// stdEngine 构造 3 个组、每组 2 名评卷人的标准引擎（阈值 10、步长 2）。
func stdEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(Config{Threshold: 10, Step: 2})
	for _, g := range []Grader{
		{ID: "gA1", Group: "A", DailyQuota: 5},
		{ID: "gA2", Group: "A", DailyQuota: 5},
		{ID: "gB1", Group: "B", DailyQuota: 5},
		{ID: "gB2", Group: "B", DailyQuota: 5},
		{ID: "gC1", Group: "C", DailyQuota: 5},
		{ID: "gC2", Group: "C", DailyQuota: 5},
	} {
		must(t, e.AddGrader(g))
	}
	return e
}

func taskFor(e *Engine, paperID, grader string) string {
	for _, tv := range e.Tasks(paperID) {
		if tv.Grader == grader && tv.Status == StatusOpen {
			return tv.ID
		}
	}
	return ""
}

func submitGrader(t *testing.T, e *Engine, paperID, grader string, score int) {
	t.Helper()
	tid := taskFor(e, paperID, grader)
	if tid == "" {
		t.Fatalf("no open task for grader %s on %s", grader, paperID)
	}
	must(t, e.Submit(tid, score))
}

// 分差恰等于阈值 => 一致，终分为平均。
func TestThresholdEqualIsConsensus(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	if tasks[0].Group == tasks[1].Group {
		t.Fatalf("初评人必须不同组: %+v", tasks)
	}
	must(t, e.Submit(tasks[0].ID, 60))
	must(t, e.Submit(tasks[1].ID, 50))
	fv, ok := e.FinalScore("p1")
	// 均值 55 在 step=2 的半网格上，相邻点 54/56，向满分取 56
	if !ok || fv.Score != 56 {
		t.Fatalf("diff==threshold 应为终分平均(靠拢)56, got %+v ok=%v", fv, ok)
	}
}

// 平均不落在网格上 => 向满分方向靠拢（ceil）。
func TestAverageRoundsTowardFull(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	must(t, e.Submit(tasks[0].ID, 50))
	must(t, e.Submit(tasks[1].ID, 54)) // diff 4 <= 10；均值 52，恰为网格 => 52
	fv, _ := e.FinalScore("p1")
	if fv.Score != 52 {
		t.Fatalf("均值 52 应在网格上, got %d", fv.Score)
	}

	// 单独验证半网格向满分靠拢：50 与 55（55=27.5*2 非网格，改用跨步长场景）
	// step=2 时两奇值均值会落在半网格。
	e2 := NewEngine(Config{Threshold: 100, Step: 2})
	must(t, e2.AddGrader(Grader{ID: "a", Group: "A", DailyQuota: 5}))
	must(t, e2.AddGrader(Grader{ID: "b", Group: "B", DailyQuota: 5}))
	must(t, e2.AddPaper(Paper{ID: "q1", Question: "q", MaxScore: 100, Student: "z1"}))
	ts := e2.Tasks("q1")
	must(t, e2.Submit(ts[0].ID, 50))
	must(t, e2.Submit(ts[1].ID, 52)) // 均值 51 => 半网格相邻点 50/52，向满分取 52
	if fv2, _ := e2.FinalScore("q1"); fv2.Score != 52 {
		t.Fatalf("均值 51 应向满分靠拢为 52, got %d", fv2.Score)
	}
}

// 仲裁：仲裁分与两初评等距时取较高初评平均。
func TestArbitrationTiePicksHigherInitial(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	must(t, e.Submit(tasks[0].ID, 50))
	must(t, e.Submit(tasks[1].ID, 80)) // diff 30 > 10 => 仲裁
	var arb TaskView
	for _, tv := range e.Tasks("p1") {
		if tv.Role == "arbiter" {
			arb = tv
		}
	}
	if arb.Group == tasks[0].Group || arb.Group == tasks[1].Group {
		t.Fatalf("仲裁人必须来自第三个组")
	}
	must(t, e.Submit(arb.ID, 70)) // 距 50 为 20、距 80 为 10；等距场景另测
	fv, _ := e.FinalScore("p1")
	// 70 与 80 均值 75 落在半网格，向满分取 76
	if fv.Score != 76 {
		t.Fatalf("应取更近的较高初评 80 与 70 平均并靠拢=76, got %d", fv.Score)
	}

	// 等距：仲裁分距两初评相等时取较高初评。另起一例：初评 40/60，仲裁 50。
	e3 := NewEngine(Config{Threshold: 10, Step: 2})
	for _, g := range []Grader{
		{ID: "a", Group: "A", DailyQuota: 5},
		{ID: "b", Group: "B", DailyQuota: 5},
		{ID: "c", Group: "C", DailyQuota: 5},
	} {
		must(t, e3.AddGrader(g))
	}
	must(t, e3.AddPaper(Paper{ID: "pe", Question: "q", MaxScore: 100, Student: "se"}))
	te := e3.Tasks("pe")
	must(t, e3.Submit(te[0].ID, 40))
	must(t, e3.Submit(te[1].ID, 60))
	var arb2 TaskView
	for _, tv := range e3.Tasks("pe") {
		if tv.Role == "arbiter" {
			arb2 = tv
		}
	}
	must(t, e3.Submit(arb2.ID, 50)) // 距 40/60 均为 10，取较高 60 => 均值 55 靠拢为 56
	fve, _ := e3.FinalScore("pe")
	if fve.Score != 56 {
		t.Fatalf("等距应取较高初评 60 与 50 平均(靠拢)=56, got %d", fve.Score)
	}
	if !strings.Contains(fve.Reason, "等距") {
		t.Fatalf("终分依据应说明等距取舍, got %s", fve.Reason)
	}
}

// 仲裁分与两初评之差均超过阈值 => 终分为仲裁分本身。
func TestArbitrationBothFarUsesArbiter(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	must(t, e.Submit(tasks[0].ID, 0))
	must(t, e.Submit(tasks[1].ID, 30)) // diff 30 > 10
	var arb TaskView
	for _, tv := range e.Tasks("p1") {
		if tv.Role == "arbiter" {
			arb = tv
		}
	}
	must(t, e.Submit(arb.ID, 100)) // 距 0 为 100、距 30 为 70，均 > 10
	fv, _ := e.FinalScore("p1")
	if fv.Score != 100 {
		t.Fatalf("两差均超阈值应取仲裁分 100, got %d", fv.Score)
	}
}

// 撤回后重新分配必须排除本人，已完成的另一份初评保留。
func TestWithdrawReassignExcludesSelf(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	first, second := tasks[0], tasks[1]
	must(t, e.Submit(first.ID, 40))
	must(t, e.Withdraw(second.ID))
	var replacement *TaskView
	for _, tv := range e.Tasks("p1") {
		if tv.ID == second.ID && tv.Status != StatusWithdrawn {
			t.Fatalf("原任务应保持已撤回")
		}
		if tv.Role == "second" && tv.Status == StatusOpen {
			replacement = &tv
		}
	}
	if replacement == nil {
		t.Fatalf("应出现重分配的第二初评任务")
	}
	if replacement.Grader == second.Grader {
		t.Fatalf("重分配不得再选中原评卷人 %s", second.Grader)
	}
	if replacement.Group == first.Group {
		t.Fatalf("重分配仍须与保留的初评异组")
	}
	submitGrader(t, e, "p1", replacement.Grader, 44)
	fv, _ := e.FinalScore("p1")
	if fv.Score != 42 {
		t.Fatalf("40 与 44 平均=42, got %d", fv.Score)
	}
}

// 停用评卷人：未完成任务重分配，已提交评分保留，同组他人不受影响。
func TestDeactivateKeepsSubmitted(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	must(t, e.Submit(tasks[0].ID, 40))
	deactivated := tasks[1].Grader
	must(t, e.Deactivate(deactivated))
	for _, tv := range e.Tasks("p1") {
		if tv.Grader == deactivated && tv.Status == StatusOpen {
			t.Fatalf("停用者不得留有待提交任务")
		}
	}
	must(t, e.AddPaper(Paper{ID: "p2", Question: "q", MaxScore: 100, Student: "s2"}))
	var replaced TaskView
	for _, tv := range e.Tasks("p1") {
		if tv.Role == "second" && tv.Status == StatusOpen {
			replaced = tv
		}
	}
	if replaced.Grader == deactivated {
		t.Fatalf("停用者不得再被选中")
	}
	submitGrader(t, e, "p1", replaced.Grader, 44)
	fv, _ := e.FinalScore("p1")
	if fv.Score != 42 {
		t.Fatalf("已提交的 40 应保留，40/44 => 42, got %d", fv.Score)
	}
	if errKind(e.Deactivate(deactivated)) != KindInactive {
		t.Fatalf("重复停用应报 KindInactive")
	}
}

// 配额恰满：达到配额后新答卷待分配；新评卷人可用后被领走。
func TestQuotaExactFull(t *testing.T) {
	e := NewEngine(Config{Threshold: 10, Step: 1})
	must(t, e.AddGrader(Grader{ID: "a", Group: "A", DailyQuota: 1}))
	must(t, e.AddGrader(Grader{ID: "b1", Group: "B", DailyQuota: 1}))
	must(t, e.AddGrader(Grader{ID: "b2", Group: "B", DailyQuota: 1}))
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 10, Student: "s1"}))
	must(t, e.AddPaper(Paper{ID: "p2", Question: "q", MaxScore: 10, Student: "s2"}))
	found := false
	for _, id := range e.Pending() {
		if id == "p2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("配额恰满后 p2 应待分配, pending=%v", e.Pending())
	}
	must(t, e.AddGrader(Grader{ID: "a2", Group: "A", DailyQuota: 1}))
	for _, id := range e.Pending() {
		if id == "p2" {
			t.Fatalf("新评卷人可用后待分配答卷应被领走")
		}
	}
}

// 回避约束：名下学生的答卷永不分给该评卷人。
func TestAvoidance(t *testing.T) {
	e := NewEngine(Config{Threshold: 10, Step: 1})
	must(t, e.AddGrader(Grader{ID: "a", Group: "A", DailyQuota: 5, Students: []string{"s1"}}))
	must(t, e.AddGrader(Grader{ID: "b", Group: "B", DailyQuota: 5}))
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 10, Student: "s1"}))
	if len(e.Pending()) == 0 {
		t.Fatalf("回避导致 A 组无候选，答卷应待分配")
	}
	must(t, e.AddGrader(Grader{ID: "a2", Group: "A", DailyQuota: 5}))
	for _, tv := range e.Tasks("p1") {
		if tv.Grader == "a" {
			t.Fatalf("回避人 a 不得被选中")
		}
	}
}

// 同一评卷人对同一学生跨题目只能出现一次（任务有效期间）。
func TestSameStudentOnce(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q1", MaxScore: 100, Student: "s1"}))
	firstSet := map[string]bool{}
	for _, tv := range e.Tasks("p1") {
		firstSet[tv.Grader] = true
	}
	must(t, e.AddPaper(Paper{ID: "p2", Question: "q2", MaxScore: 100, Student: "s1"}))
	for _, tv := range e.Tasks("p2") {
		if firstSet[tv.Grader] {
			t.Fatalf("评卷人 %s 对同一学生 s1 出现第二次", tv.Grader)
		}
	}
}

// 终分产生后任何提交均拒绝；重复提交不覆盖；被拒操作不入审计。
func TestFinalImmutableAndResubmit(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tasks := e.Tasks("p1")
	must(t, e.Submit(tasks[0].ID, 40))
	if errKind(e.Submit(tasks[0].ID, 99)) != KindTaskState {
		t.Fatalf("重复提交应 KindTaskState")
	}
	auditBefore := len(e.Audit())
	must(t, e.Submit(tasks[1].ID, 44))
	if errKind(e.Submit(tasks[0].ID, 80)) != KindTaskState {
		t.Fatalf("终分后提交应 KindTaskState")
	}
	fv, _ := e.FinalScore("p1")
	if fv.Score != 42 {
		t.Fatalf("被拒提交不得改变终分, got %d", fv.Score)
	}
	if len(e.Audit()) != auditBefore+2 { // 仅第二份 submit + final 两条
		t.Fatalf("被拒绝操作不得入审计: before=%d after=%d", auditBefore, len(e.Audit()))
	}
}

// 分数越界 / 不在步长网格。
func TestScoreValidation(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tid := e.Tasks("p1")[0].ID
	if errKind(e.Submit(tid, -2)) != KindScore {
		t.Fatalf("负分应 KindScore")
	}
	if errKind(e.Submit(tid, 102)) != KindScore {
		t.Fatalf("超满分应 KindScore")
	}
	if errKind(e.Submit(tid, 53)) == KindOK {
		// 53 对 step=2 不在网格
		t.Fatalf("非网格分数应拒绝")
	} else if k := errKind(e.Submit(tid, 53)); k != KindScore {
		t.Fatalf("非网格分数应 KindScore, got %d", k)
	}
	must(t, e.Submit(tid, 54))
}

// 选取评卷人的开销必须以可验证方式证明不随已完成任务总数、答卷总数增长。
// 做法：固定评卷人规模 G，在“小历史”与“大历史”（完成任务数相差 100 倍）
// 两种状态下分别统计一次选取的资格谓词求值次数，二者必须相等。
// 这直接证明单次选取只扫描评卷人结构（O(G)），与历史任务/答卷数量无关。
func TestSelectionCostIndependentOfHistory(t *testing.T) {
	measure := func(completedTasks int) int {
		e := NewEngine(Config{Threshold: 10, Step: 1})
		// 3 组各 4 名评卷人，配额足够大以保证历史完成后仍可领取。
		for _, grp := range []string{"A", "B", "C"} {
			for i := 0; i < 4; i++ {
				id := grp + string(rune('0'+i))
				must(t, e.AddGrader(Grader{ID: id, Group: grp, DailyQuota: 1000000}))
			}
		}
		// 制造 completedTasks 个已完成（已提交）任务，分布在大量答卷上。
		for i := 0; i < completedTasks; i++ {
			pid := "hist" + itoa(i)
			must(t, e.AddPaper(Paper{ID: pid, Question: "q", MaxScore: 10, Student: "h" + itoa(i%50)}))
			for _, tv := range e.Tasks(pid) {
				if tv.Status == StatusOpen {
					must(t, e.Submit(tv.ID, 5))
				}
			}
		}
		must(t, e.AddPaper(Paper{ID: "probe", Question: "q", MaxScore: 10, Student: "probe-student"}))
		// probe 的两名初评已自动发放；现在撤回第一初评再统计重选代价。
		first := e.Tasks("probe")[0]
		must(t, e.Withdraw(first.ID))
		// 撤回后引擎已自动重选；改为直接度量一次底层选择的谓词次数：
		ps := e.papers["probe"]
		busy := map[string]struct{}{}
		for _, tk := range ps.tasks {
			busy[tk.Grader] = struct{}{}
		}
		var counts int
		e.mu.Lock()
		e.selectCandidate(e.allGroups(nil), busy, "probe-student", &counts)
		e.mu.Unlock()
		return counts
	}
	small := measure(1)
	large := measure(100)
	if small <= 0 {
		t.Fatalf("谓词计数应为正, got %d", small)
	}
	if small != large {
		t.Fatalf("选取代价随历史增长: 小历史=%d 次谓词, 大历史=%d", small, large)
	}
	if small > 12 { // 3 组 x 4 评卷人，每组至多扫描到首个合格者
		t.Fatalf("谓词次数应受评卷人数上界约束, got %d", small)
	}
}

// 并发冒烟：大量并发操作后系统状态自洽，结果等价于某串行顺序。
func TestConcurrentSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	e := stdEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pid := "cp" + itoa(i)
			_ = e.AddPaper(Paper{ID: pid, Question: "q", MaxScore: 100, Student: "cs" + itoa(i%4)})
			for _, tv := range e.Tasks(pid) {
				_ = e.Submit(tv.ID, 50)
			}
		}(i)
	}
	wg.Wait()
	// 不变量：每份已终分答卷恰有 2 份有效评分且两名初评异组；
	// 未终分者要么待分配，要么仍有 open 任务（同人单次约束下可能暂无候选）。
	for i := 0; i < 30; i++ {
		pid := "cp" + itoa(i)
		var groups []string
		hasOpen := false
		for _, tv := range e.Tasks(pid) {
			if tv.Status == StatusOpen {
				hasOpen = true
			}
			if tv.Status == StatusSubmitted {
				groups = append(groups, tv.Group)
			}
		}
		if fv, ok := e.FinalScore(pid); ok {
			_ = fv
			if len(groups) != 2 || groups[0] == groups[1] {
				t.Fatalf("终分答卷 %s 应有两份异组初评, got %v", pid, groups)
			}
		} else if !hasOpen {
			found := false
			for _, x := range e.Pending() {
				if x == pid {
					found = true
				}
			}
			if !found {
				t.Fatalf("未终分答卷 %s 既无 open 任务也不在待分配集合", pid)
			}
		}
	}
}

// 回避与“同人单次”约束同时触发时的归因：两者都只是候选过滤条件，
// 当所有候选均被过滤时，结果归因为 KindNoCandidate。
func TestAvoidanceAndRepeatAttribution(t *testing.T) {
	e := NewEngine(Config{Threshold: 10, Step: 1})
	// A 组只有 a；a 回避 s1。B 组 b 不回避 s1。
	must(t, e.AddGrader(Grader{ID: "a", Group: "A", DailyQuota: 5, Students: []string{"s1"}}))
	must(t, e.AddGrader(Grader{ID: "b", Group: "B", DailyQuota: 5}))
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 10, Student: "s1"}))
	if len(e.Pending()) != 1 || e.Pending()[0] != "p1" {
		t.Fatalf("A 组仅回避人，初评配不齐 => p1 待分配")
	}
	// 引擎不得记录这次失败的分配尝试以外的状态：再录入合格 A 组评卷人后可正常分配
	must(t, e.AddGrader(Grader{ID: "a2", Group: "A", DailyQuota: 5}))
	if len(e.Tasks("p1")) != 2 {
		t.Fatalf("新候选可用后应自动配齐两名初评, got %d", len(e.Tasks("p1")))
	}
	// 再新增一份 s1 的答卷：b 在 p1 上对 s1 已有有效任务且是 B 组唯一候选，
	// “同人单次”约束（叠加 b 并不回避该生）单独导致 B 组无合格候选。
	must(t, e.AddPaper(Paper{ID: "p3", Question: "q3", MaxScore: 10, Student: "s1"}))
	found := false
	for _, id := range e.Pending() {
		if id == "p3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("同人单次导致 B 组无合格候选，p3 应待分配")
	}
	// 显式重分配（撤回）在候选全被回避/重复过滤时必须归因 KindNoCandidate：
	// 构造 A 组唯一候选 a2 被撤回且永久排除、a 回避 s1 的局面。
	var openA string
	for _, tv := range e.Tasks("p1") {
		if tv.Group == "A" && tv.Status == StatusOpen {
			openA = tv.ID
		}
	}
	if openA != "" {
		if k := errKind(e.Withdraw(openA)); k != KindNoCandidate {
			t.Fatalf("A 组仅剩回避人时重分配应 KindNoCandidate, got %d", k)
		}
	}
}

// 拒绝优先级逐对验证：构造同一操作同时触发两类错误，断言取较高优先级类别。
func TestErrorPriorityPairs(t *testing.T) {
	e := stdEngine(t)
	must(t, e.AddPaper(Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"}))
	tid := e.Tasks("p1")[0].ID

	// 参数非法 vs 任务不存在：空 ID 即使“不存在”也优先报参数非法
	if k := errKind(e.Submit("", 10)); k != KindInvalidParam {
		t.Fatalf("空ID 应 KindInvalidParam, got %d", k)
	}
	if k := errKind(e.Withdraw("")); k != KindInvalidParam {
		t.Fatalf("空ID 撤回应 KindInvalidParam, got %d", k)
	}
	if k := errKind(e.Deactivate("")); k != KindInvalidParam {
		t.Fatalf("空ID 停用应 KindInvalidParam, got %d", k)
	}
	// 任务不存在 vs 分数非法：不存在的任务携带越界分数仍报不存在
	if k := errKind(e.Submit("T999", 1<<30)); k != KindNotFound {
		t.Fatalf("不存在任务应 KindNotFound, got %d", k)
	}
	// 不存在 vs 停用：不存在的评卷人停用报不存在
	if k := errKind(e.Deactivate("ghost")); k != KindNotFound {
		t.Fatalf("不存在评卷人应 KindNotFound, got %d", k)
	}
	// 停用 vs 任务状态：停用持有 open 任务的评卷人后，对其任务提交应报停用
	victim := e.Tasks("p1")[1]
	must(t, e.Deactivate(victim.Grader))
	if k := errKind(e.Submit(victim.ID, 30)); k != KindInactive {
		t.Fatalf("停用评卷人的任务提交应 KindInactive, got %d", k)
	}
	if k := errKind(e.Withdraw(victim.ID)); k != KindInactive {
		t.Fatalf("停用评卷人的任务撤回应 KindInactive, got %d", k)
	}
	// 任务状态（重复提交）vs 分数非法：重复提交即便分数非法也报状态
	must(t, e.Submit(tid, 40))
	if k := errKind(e.Submit(tid, 1<<30)); k != KindTaskState {
		t.Fatalf("重复提交应优先 KindTaskState, got %d", k)
	}
	// 已撤回任务 vs 分数非法
	e2 := stdEngine(t)
	must(t, e2.AddPaper(Paper{ID: "p2", Question: "q", MaxScore: 100, Student: "s2"}))
	wid := e2.Tasks("p2")[0].ID
	must(t, e2.Withdraw(wid))
	if k := errKind(e2.Submit(wid, -9)); k != KindTaskState {
		t.Fatalf("已撤回任务提交应优先 KindTaskState, got %d", k)
	}
}
