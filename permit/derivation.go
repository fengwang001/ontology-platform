package permit

// suppReq 一次补正请求及其恢复/超时信息。
type suppReq struct {
	notifyDay int // 要求补正（暂停）日
	deadline  int // 补正期限最后一日（工作日）
	resumeDay int // 实际提交补正并恢复计时日；0 表示未恢复
}

// segment 一个计时区间（左开右闭 (left, right]，按工作日计数）。
// 首个区间 left=startDay（起算日是启动日的次工作日，故不含启动当日）；
// 恢复区间 left=resumeDay（恢复当日计入已用）。
type segment struct {
	left     int
	right    int  // 0 表示仍在计时，右边界在推导时取当前日
	inclLeft bool // 首个区间 false（启动当日不计）；恢复区间 true（恢复当日计入）
}

// stageState 单个环节在某推导时刻的运行时状态。
type stageState struct {
	def      *StageDef
	startDay int
	started  bool

	status    StageStatus
	passDay   int
	rejectDay int

	reqs []suppReq // 已受理的补正请求（按通知日）
	segs []segment // 已闭合或当前的计时区间

	overtime bool
	dueDay   int
	hasDue   bool
	remain   int
}

// snapshot 一件许可在某推导时刻的完整快照。
type snapshot struct {
	c           *permitCase
	at          int
	stages      map[string]*stageState
	outcome     CaseOutcome
	finalDay    int
	hasFinal    bool
	firstRej    int // 首个不通过日（决定未启动后继的封锁线）
	withdrawn   bool
	withdrawDay int
}

// derive 依据不可变事件日志纯函数地推导许可在 day 时刻的快照。
// 不修改任何状态：所有“超时默认通过/补正逾期”均为惰性派生，
// 因此其开销只与本许可环节数与本许可事件数相关，与全局在办许可数无关。
func (s *Service) derive(c *permitCase, day int) *snapshot {
	snap := &snapshot{c: c, at: day, stages: map[string]*stageState{}}
	for _, id := range c.order {
		snap.stages[id] = &stageState{def: c.stages[id]}
	}
	// 事件按发生顺序（seq）处理；派生只依赖 [受理日, day] 的事件。
	for _, ev := range c.events {
		if ev.day > day {
			continue
		}
		snap.apply(s.cal, ev, s.suppWorkdays)
	}
	snap.finalize(s.cal, day, s.suppWorkdays)
	return snap
}

// startDayOf 计算环节启动日：无前置为受理当日，否则为全部前置通过日的最大值。
// 前置存在但未全部通过时返回 0 且 started=false。
func (sn *snapshot) startDayOf(st *stageState) (int, bool) {
	if len(st.def.Prereqs) == 0 {
		return sn.c.accepted, true
	}
	latest := 0
	for _, pid := range st.def.Prereqs {
		p := sn.stages[pid]
		if p.status != StageApproved {
			return 0, false
		}
		if p.passDay > latest {
			latest = p.passDay
		}
	}
	return latest, true
}

// apply 重放一条显式事件。
func (sn *snapshot) apply(cal *Calendar, ev *operation, suppWD int) {
	switch ev.kind {
	case opAccept:
		for _, st := range sn.stages {
			if len(st.def.Prereqs) == 0 {
				sn.start(cal, st, sn.c.accepted)
			}
		}
	case opWithdraw:
		sn.withdrawn = true
		sn.withdrawDay = ev.day
		for _, st := range sn.stages {
			if st.status == StageNotStarted {
				st.status = StageWithdrawn
			}
		}
	case opSubmitSupp:
		st := sn.stages[ev.stageID]
		if st != nil && len(st.reqs) > 0 {
			r := &st.reqs[len(st.reqs)-1]
			if r.resumeDay == 0 && ev.day <= r.deadline {
				r.resumeDay = ev.day
				st.segs = append(st.segs, segment{left: ev.day, inclLeft: true})
				st.status = StageActive
			}
		}
	case opDecide:
		st := sn.stages[ev.stageID]
		if st == nil {
			return
		}
		if !sn.ensureStarted(cal, st) {
			return
		}
		switch ev.decision {
		case DecideApprove:
			if st.status == StageActive || st.status == StagePaused {
				sn.closeStage(cal, st, StageApproved, ev.day)
			}
		case DecideReject:
			if st.status == StageActive || st.status == StagePaused {
				sn.closeStage(cal, st, StageRejected, ev.day)
			}
		case DecideSupplement:
			if st.status == StageActive {
				st.segs = appendPause(st.segs, ev.day)
				st.reqs = append(st.reqs, suppReq{
					notifyDay: ev.day,
					deadline:  cal.NthWorkingOnOrAfter(ev.day+1, suppWD),
				})
				st.status = StagePaused
			}
		}
	}
}

func appendPause(segs []segment, day int) []segment {
	if len(segs) == 0 {
		return segs
	}
	segs[len(segs)-1].right = day
	return segs
}

func (sn *snapshot) start(cal *Calendar, st *stageState, day int) {
	st.started = true
	st.startDay = day
	st.status = StageActive
	st.segs = []segment{{left: day, inclLeft: false}}
}

// ensureStarted 惰性启动因前置刚满足而在本事件日应启动的环节。
func (sn *snapshot) ensureStarted(cal *Calendar, st *stageState) bool {
	if st.status != StageNotStarted {
		return st.status == StageActive || st.status == StagePaused
	}
	day, ok := sn.startDayOf(st)
	if !ok {
		return false
	}
	sn.start(cal, st, day)
	return true
}

// closeStage 办结环节。若办结时已超过届满日（非超时默认的手动办结），
// 超时标记保留且不可清除；办结后剩余时限记为 0。
func (sn *snapshot) closeStage(cal *Calendar, st *stageState, status StageStatus, day int) {
	if len(st.segs) > 0 {
		last := &st.segs[len(st.segs)-1]
		if last.right == 0 {
			last.right = day
		}
		used := sn.usedWorkdays(cal, st, day)
		st.hasDue = true
		st.dueDay = sn.segmentDue(cal, st, len(st.segs)-1)
		if used > st.def.DueWorkdays && !st.def.AutoPass {
			st.overtime = true
		}
	}
	st.remain = 0
	st.status = status
	if status == StageApproved {
		st.passDay = day
	} else {
		st.rejectDay = day
	}
}

// finalize 在给定 day 上完成所有惰性派生（不写入事件日志）。
// 启动、补正逾期、超时默认通过可能级联，故定点迭代直至状态稳定。
func (sn *snapshot) finalize(cal *Calendar, day, suppWD int) {
	// 撤回后一切停止：不再启动、计时、超时默认或补正逾期，直接冻结终局。
	if sn.withdrawn {
		sn.resolveOutcome()
		return
	}
	for {
		changed := false

		// 1) 依拓扑序惰性启动满足前置的环节（启动日 <= day）。
		for _, id := range sn.c.order {
			st := sn.stages[id]
			if st.status == StageNotStarted {
				if sd, ok := sn.startDayOf(st); ok && sd <= day {
					sn.start(cal, st, sd)
					changed = true
				}
			}
		}

		// 2) 补正逾期未提交：在补正期限届满的下一工作日视为不通过。
		for _, id := range sn.c.order {
			st := sn.stages[id]
			if st.status != StagePaused || len(st.reqs) == 0 {
				continue
			}
			r := &st.reqs[len(st.reqs)-1]
			if r.resumeDay == 0 && day > r.deadline {
				fail := cal.NextWorkingAfter(r.deadline)
				if fail <= day {
					sn.closeStage(cal, st, StageRejected, fail)
					changed = true
				}
			}
		}

		// 3) 在办环节的时限、超时标记与超时默认通过（惰性派生）。
		for _, id := range sn.c.order {
			st := sn.stages[id]
			if st.status == StageActive {
				used := sn.usedWorkdays(cal, st, day)
				st.hasDue = true
				st.dueDay = sn.segmentDue(cal, st, len(st.segs)-1)
				st.remain = st.def.DueWorkdays - used
				if st.remain < 0 {
					st.remain = 0
				}
				if used > st.def.DueWorkdays {
					if st.def.AutoPass {
						// 届满日后第一个操作触及时视为通过，通过时刻为届满日下一工作日。
						passDay := cal.NextWorkingAfter(st.dueDay)
						st.overtime = false
						sn.closeStage(cal, st, StageApproved, passDay)
						changed = true
					} else if !st.overtime {
						st.overtime = true
					}
				}
			} else if st.status == StagePaused {
				// 暂停：剩余时限冻结在暂停当日，暂停期间不触发超时。
				measure := st.segs[len(st.segs)-1].right
				used := sn.usedWorkdays(cal, st, measure)
				st.hasDue = true
				st.dueDay = sn.segmentDue(cal, st, len(st.segs)-1)
				st.remain = st.def.DueWorkdays - used
				if st.remain < 0 {
					st.remain = 0
				}
				if !st.def.AutoPass && measure > st.dueDay {
					st.overtime = true
				}
			}
		}

		if !changed {
			break
		}
	}

	// 4) 封锁线：被拒环节在依赖图上的后代永不启动；
	//    与被拒环节无依赖关系的并行环节（及其后继）照常启动、办理。
	firstRej := 0
	for _, id := range sn.c.order {
		st := sn.stages[id]
		if st.status == StageRejected && (firstRej == 0 || st.rejectDay < firstRej) {
			firstRej = st.rejectDay
		}
	}
	sn.firstRej = firstRej
	if firstRej > 0 {
		// 拓扑序传播：若任一前置已 Rejected/Blocked，则该未启动环节 Blocked。
		for _, id := range sn.c.order {
			st := sn.stages[id]
			if st.status != StageNotStarted {
				continue
			}
			for _, pid := range st.def.Prereqs {
				ps := sn.stages[pid]
				if ps.status == StageBlocked || ps.status == StageRejected {
					st.status = StageBlocked
					break
				}
			}
		}
	}

	// 5) 整件终局。
	sn.resolveOutcome()
}

// resolveOutcome 依据各环节状态确定整件结论与终局时刻。
func (sn *snapshot) resolveOutcome() {
	switch {
	case sn.withdrawn:
		for _, st := range sn.stages {
			if st.status == StageActive || st.status == StagePaused || st.status == StageNotStarted {
				st.status = StageWithdrawn
			}
		}
		sn.outcome = OutcomeWithdrawn
		sn.hasFinal = true
		sn.finalDay = sn.withdrawDay
	case sn.firstRej > 0:
		// 终局结论与终局时刻在首个不通过时即锁定为不予许可；
		// 在办并行环节可继续办结（收尾），但不改变结论。
		sn.outcome = OutcomeDenied
		sn.finalDay = sn.firstRej
		active := false
		for _, st := range sn.stages {
			if st.status == StageActive || st.status == StagePaused {
				active = true
			}
		}
		sn.hasFinal = !active
	default:
		allApproved := true
		last := 0
		for _, st := range sn.stages {
			if st.status != StageApproved {
				allApproved = false
			}
			if st.passDay > last {
				last = st.passDay
			}
		}
		if allApproved {
			sn.outcome = OutcomeGranted
			sn.hasFinal = true
			sn.finalDay = last
		}
	}
}

// usedWorkdays 截至 day（在办当日计入）已用掉的工作日数。
// 首个计时段 (start, day]：起算日是启动日的次工作日；
// 暂停段 (resume, pause] 与恢复段 (resume, day]：恢复/暂停当日计入。
func (sn *snapshot) usedWorkdays(cal *Calendar, st *stageState, day int) int {
	total := 0
	for _, sg := range st.segs {
		right := sg.right
		if right == 0 || right > day {
			right = day
		}
		total += sn.segUsed(cal, sg, right)
	}
	return total
}

// segmentDue 计算某计时段的届满日：只取决于“进入该段时尚余额度”，
// 与当前是否已超时无关，因此超时后仍能还原出真实届满日。
//   - 首个区间（启动段）：起算日为启动日次工作日，计 (left, ..]。
//   - 恢复区间：恢复当日计入，计 [left, ..]。
func (sn *snapshot) segmentDue(cal *Calendar, st *stageState, segIdx int) int {
	quota := st.def.DueWorkdays
	for i := 0; i < segIdx; i++ {
		prev := st.segs[i]
		quota -= sn.segUsed(cal, prev, prev.right)
	}
	seg := st.segs[segIdx]
	if quota <= 0 {
		// 进入该段前时限已用尽：届满日落在该段左边界之前的最近工作日。
		return cal.PrevWorking(seg.left - 1)
	}
	if seg.inclLeft {
		return cal.NthWorkingOnOrAfter(seg.left, quota)
	}
	return cal.AddWorking(seg.left, quota)
}

// segUsed 计算单个计时段在给定截止日上消耗的工作日。
func (sn *snapshot) segUsed(cal *Calendar, sg segment, right int) int {
	if sg.inclLeft {
		if right < sg.left {
			return 0
		}
		return cal.WorkingIn(sg.left, right)
	}
	if right <= sg.left {
		return 0
	}
	return cal.WorkingBetween(sg.left, right)
}

// topoOrder 返回无环环节定义的一个拓扑序（卡恩算法），非法时返回错误。
func topoOrder(t *PermitType) ([]string, error) {
	n := len(t.Stages)
	idx := make(map[string]int, n)
	for i := range t.Stages {
		st := &t.Stages[i]
		if st.ID == "" {
			return nil, errf(ErrInvalidParam, "stage with empty id in type %q", t.ID)
		}
		if st.Department == "" {
			return nil, errf(ErrInvalidParam, "stage %q missing department", st.ID)
		}
		if st.DueWorkdays < 1 {
			return nil, errf(ErrInvalidParam, "stage %q due workdays must be >=1", st.ID)
		}
		if _, dup := idx[st.ID]; dup {
			return nil, errf(ErrInvalidParam, "duplicate stage %q", st.ID)
		}
		idx[st.ID] = i
	}
	indeg := make([]int, n)
	adj := make([][]int, n)
	for i := range t.Stages {
		st := &t.Stages[i]
		seen := map[string]bool{}
		for _, p := range st.Prereqs {
			if seen[p] {
				continue
			}
			seen[p] = true
			j, ok := idx[p]
			if !ok {
				return nil, errf(ErrInvalidParam, "stage %q prereq %q not found", st.ID, p)
			}
			if j == i {
				return nil, errf(ErrInvalidParam, "stage %q depends on itself", st.ID)
			}
			adj[j] = append(adj[j], i)
			indeg[i]++
		}
	}
	queue := make([]int, 0, n)
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]string, 0, n)
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, t.Stages[i].ID)
		for _, j := range adj[i] {
			indeg[j]--
			if indeg[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(order) != n {
		return nil, errf(ErrInvalidParam, "type %q prereq graph has cycle", t.ID)
	}
	return order, nil
}
