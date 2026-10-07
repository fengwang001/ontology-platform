package approval

// 本文件实现许可单（permit）的内部模型。
//
// 核心思路：事件溯源 + 惰性派生。
//   - 每个环节只追加事件（启动/通过/不通过/要求补正/提交补正/撤回/终止），
//     事件携带确定性的日序号，绝不修改。
//   - 超时默认通过、补正期满视为不通过、后继启动、他环节终止等"派生"事实，
//     其生效日本身就是确定的，因此在任意操作触及该许可单时按当日序
//     物化（materialize）即可，无需后台扫描，开销与在办许可总数无关。
//   - 历史查询 = 取截至查询日的全部事件重放求值，与实际状态必然一致。

type eventKind int

const (
	evStarted eventKind = iota + 1
	evApproved
	evRejected
	evPaused  // 要求补正（通知日）
	evResumed // 提交补正（恢复计时）
	evWithdrawn
	evTerminated // 因他环节终止而未启动
)

type stageEvent struct {
	day      int
	kind     eventKind
	cause    FinishCause // 仅办结事件使用
	corrLast int         // 仅 evPaused 使用：补正期限最后一日
}

type stage struct {
	def    StageDef
	events []stageEvent
}

type licenseType struct {
	def    LicenseTypeDef
	stages map[string]StageDef
	order  []string // 环节 ID，定义顺序
	preds  map[string][]string
}

type permit struct {
	typ          *licenseType
	acceptDay    int
	applicant    string
	withdrawnDay int // -1 表示未撤回
	stages       map[string]*stage
	order        []string
}

// stageState 由事件序列重放得到的环节在某一时刻的内部状态。
type stageState struct {
	status      StageStatus
	startDay    int
	countFrom   int  // 当前计时段的锚定日（启动日/恢复日）
	remaining   int  // 锚定日时的剩余时限（工作日）
	corrLast    int  // 补正期限最后一日，非补正中为 -1
	corrections int  // 已要求补正次数
	finishDay   int
	cause       FinishCause
	overdue     bool // 超时标记：仅不适用超时默认的环节会置位，置位后不可清除
}

// walk 重放 events 中 day 不晚于指定日的全部事件，得到该日状态。
func walk(events []stageEvent, def StageDef, cal *calendar, day int) stageState {
	st := stageState{
		status:    StatusNotStarted,
		startDay:  -1,
		countFrom: -1,
		remaining: def.Limit,
		corrLast:  -1,
		finishDay: -1,
	}
	expiry := func() int { return cal.addWorkdays(st.countFrom, st.remaining) }
	for _, e := range events {
		if e.day > day {
			continue
		}
		switch e.kind {
		case evStarted:
			st.status = StatusInProgress
			st.startDay = e.day
			st.countFrom = e.day
			st.remaining = def.Limit
		case evPaused:
			// 暂停当日已用掉的计入；超时标记在暂停时固化
			if !def.TimeoutDefault && e.day > expiry() {
				st.overdue = true
			}
			st.remaining -= cal.workdaysBetween(st.countFrom, e.day)
			if st.remaining < 0 {
				st.remaining = 0
			}
			st.status = StatusCorrecting
			st.corrLast = e.corrLast
			st.corrections++
		case evResumed:
			st.status = StatusInProgress
			st.countFrom = e.day
			st.corrLast = -1
		case evApproved:
			if !def.TimeoutDefault && e.day > expiry() {
				st.overdue = true
			}
			st.status = StatusApproved
			st.finishDay = e.day
			st.cause = e.cause
		case evRejected:
			if !def.TimeoutDefault && st.status == StatusInProgress && e.day > expiry() {
				st.overdue = true
			}
			st.status = StatusRejected
			st.finishDay = e.day
			st.cause = e.cause
		case evWithdrawn:
			if st.status == StatusInProgress {
				st.remaining -= cal.workdaysBetween(st.countFrom, e.day)
				if st.remaining < 0 {
					st.remaining = 0
				}
			}
			st.status = StatusWithdrawn
			st.finishDay = e.day
		case evTerminated:
			st.status = StatusTerminated
			st.finishDay = e.day
		}
	}
	return st
}

// view 由内部状态生成对外视图。
func (st stageState) view(def StageDef, cal *calendar, day int) StageView {
	v := StageView{
		Status:            st.status,
		Overdue:           st.overdue,
		StartDay:          st.startDay,
		FinishDay:         st.finishDay,
		CorrectionsUsed:   st.corrections,
		CorrectionLastDay: st.corrLast,
		Cause:             st.cause,
	}
	switch st.status {
	case StatusNotStarted, StatusTerminated:
		v.Remaining = def.Limit
	case StatusInProgress:
		v.Remaining = st.remaining - cal.workdaysBetween(st.countFrom, day)
		if v.Remaining < 0 {
			v.Remaining = 0
		}
		if day > cal.addWorkdays(st.countFrom, st.remaining) {
			v.Overdue = true
		}
	case StatusCorrecting, StatusWithdrawn:
		v.Remaining = st.remaining
	case StatusApproved, StatusRejected:
		if st.cause == CauseCorrectionExpired {
			v.Remaining = st.remaining // 暂停时冻结的剩余时限
		} else {
			v.Remaining = st.remaining - cal.workdaysBetween(st.countFrom, st.finishDay)
			if v.Remaining < 0 {
				v.Remaining = 0
			}
		}
	}
	return v
}

// finalInfo 由事件集合推导整件终局：任一环节不通过即不予许可（取最早不通过日），
// 全部环节通过则准予许可（取最后通过日）。
func finalInfo(evs map[string][]stageEvent, order []string) (denied bool, denyDay int, allApproved bool, approveDay int) {
	denyDay, approveDay = -1, -1
	approved := 0
	for _, id := range order {
		for _, e := range evs[id] {
			switch e.kind {
			case evRejected:
				if denyDay < 0 || e.day < denyDay {
					denyDay = e.day
				}
				denied = true
			case evApproved:
				approved++
				if e.day > approveDay {
					approveDay = e.day
				}
			}
		}
	}
	allApproved = approved == len(order)
	return denied, denyDay, allApproved, approveDay
}

// deriveEvents 返回截至 day（含）的完整事件序列：已记录事件 + 全部派生事件。
// 纯函数，不修改许可单；materialize 与查询/校验共用同一份逻辑，保证一致性。
// 派生规则：
//   - 前置全部通过的未启动环节，在最后通过的前置通过日启动；
//   - 适用超时默认的办理中环节，在届满日的下一个工作日视为通过；
//   - 补正期满仍未补正的环节，在期限最后一日的下一日视为不通过；
//   - 任一环节不通过后，未启动环节在该不通过日终止。
func (p *permit) deriveEvents(cal *calendar, day int, stats *Stats) map[string][]stageEvent {
	evs := make(map[string][]stageEvent, len(p.stages))
	for id, st := range p.stages {
		evs[id] = append([]stageEvent(nil), st.events...)
	}
	if p.withdrawnDay >= 0 {
		return evs // 撤回后一切停止，不再派生
	}
	for changed := true; changed; {
		changed = false
		denied, denyDay, allApproved, _ := finalInfo(evs, p.order)
		if !denied && !allApproved {
			for _, id := range p.order {
				stats.StageScans++
				if len(evs[id]) > 0 {
					continue
				}
				preds := p.typ.preds[id]
				if len(preds) == 0 {
					continue // 无前置环节在受理当日已启动
				}
				start := -1
				ok := true
				for _, pr := range preds {
					ad := -1
					for _, e := range evs[pr] {
						if e.kind == evApproved {
							ad = e.day
						}
					}
					if ad < 0 {
						ok = false
						break
					}
					if ad > start {
						start = ad
					}
				}
				if ok && start <= day {
					evs[id] = append(evs[id], stageEvent{day: start, kind: evStarted})
					changed = true
				}
			}
		}
		for _, id := range p.order {
			stats.StageScans++
			def := p.typ.stages[id]
			st := walk(evs[id], def, cal, day)
			switch st.status {
			case StatusInProgress:
				if def.TimeoutDefault {
					a := cal.nextWorkday(cal.addWorkdays(st.countFrom, st.remaining))
					if a <= day {
						evs[id] = append(evs[id], stageEvent{day: a, kind: evApproved, cause: CauseTimeoutDefault})
						changed = true
					}
				}
			case StatusCorrecting:
				if st.corrLast+1 <= day {
					evs[id] = append(evs[id], stageEvent{day: st.corrLast + 1, kind: evRejected, cause: CauseCorrectionExpired})
					changed = true
				}
			}
		}
		if denied {
			for _, id := range p.order {
				stats.StageScans++
				if len(evs[id]) == 0 {
					evs[id] = append(evs[id], stageEvent{day: denyDay, kind: evTerminated})
					changed = true
				}
			}
		}
	}
	return evs
}
