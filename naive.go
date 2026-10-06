package ontology

import "strconv"

// OpKind 标识已接受操作流水的种类。
type OpKind int

const (
	OpCreate OpKind = iota + 1
	OpDisconnect
	OpResume
	OpAnswer
	OpEvent
	OpUnlock
	OpSubmit
	OpExtend
)

// AcceptedOp 是一条已接受操作流水（被拒绝的操作不入流水、不推进任何时钟）。
type AcceptedOp struct {
	Kind       OpKind
	At         int64
	Seq        int64
	Generation int64
	Credential string
	Answer     Answer
	KindText   string
}

func (s *session) appendLog(k OpKind, at, seq, gen int64, cred string, a Answer) {
	s.log = append(s.log, AcceptedOp{Kind: k, At: at, Seq: seq, Generation: gen, Credential: cred, Answer: a})
}

type nEvent struct {
	at   int64
	rank int64
	kind string
}

// naiveModel 是需求规则的朴素独立实现：保存全部已接受操作，
// 每次观察都从创建点按操作流重放状态机，并全量扫描作答表与事件表。
// 刻意不使用引擎的冻结账目、序号哈希水位和单调窗口队列。
type naiveModel struct {
	cfg       Config
	start     int64
	ops       []AcceptedOp
	events    []nEvent
	answers   map[string]Answer
	seqOwner  map[int64]string
	maxSeq    int64
	credSeq   int64
	curCred   string
	gen       int64
	budget    int64
	warnings  int
	locked    bool
	violation bool
}

func newNaiveModel(cfg Config, start int64) *naiveModel {
	return &naiveModel{
		cfg: cfg, start: start, budget: cfg.Budget, gen: 1,
		answers:  map[string]Answer{},
		seqOwner: map[int64]string{},
	}
}

func (m *naiveModel) issueCred() string {
	m.credSeq++
	return "cred-" + strconv.FormatInt(m.credSeq, 10)
}

func (m *naiveModel) endedErr() *Error {
	return errf(ErrSessionEnded, SubSettled, "session ended")
}

type stateAt struct {
	state     State
	used      int64
	paused    int64
	endAt     int64
	endReason string
	gen       int64
	cred      string
	budget    int64
}

// nPoint 是朴素重放用的状态迁移点（只在真正状态变化或结束时产生）。
type nPoint struct {
	at     int64
	state  State
	end    string
	budget int64
	gen    int64
	cred   string
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// fStair 是某时刻生效的预算阶梯。
type fStair struct{ at, budget int64 }

// findBudgetHit 在 [stairs[0].at, hi] 内求有效作答时长首次达到当时预算的时刻。
// rateZeroUntil>0 表示在该时刻之前作答速率为 0（暂停预算保护期），之后速率为 1。
// 找不到返回 -1。
func findBudgetHit(stairs []fStair, hi, rateZeroUntil int64, baseUsed int64,
	uAt func(int64) int64) int64 {
	for k, s0 := range stairs {
		nextX := hi
		if k+1 < len(stairs) {
			nextX = stairs[k+1].at
		}
		u0 := uAt(s0.at)
		if u0 >= s0.budget {
			return s0.at
		}
		var hit int64
		if s0.at < rateZeroUntil {
			hit = rateZeroUntil + (s0.budget - baseUsed)
		} else {
			hit = s0.at + (s0.budget - u0)
		}
		if hit <= nextX {
			return hit
		}
	}
	return -1
}

// buildPoints 把已接受操作流压缩为状态迁移点序列。
// Extend 只改预算、不改状态，预算变化记录在后继迁移点上；
// 为此先算出每个操作时刻生效的预算，再产生状态点。
func (m *naiveModel) buildPoints() []nPoint {
	// 预算阶梯：(时刻 -> 当时预算)。
	type bh struct {
		at, budget int64
	}
	hikes := []bh{{m.start, m.cfg.Budget}}
	curBudget := m.cfg.Budget
	for _, op := range m.ops {
		if op.Kind == OpExtend {
			curBudget += op.Seq
			hikes = append(hikes, bh{op.At, curBudget})
		}
	}
	// 任意挂钟时刻生效的预算：找到其之前最后一次 Extend 后的预算。
	budgetAt := func(at int64) int64 {
		b := m.cfg.Budget
		for _, h := range hikes {
			if h.at <= at {
				b = h.budget
			}
		}
		return b
	}

	pts := []nPoint{{at: m.start, state: StateActive, budget: m.cfg.Budget, gen: 1}}
	curGen := int64(1)
	var curCred string
	for _, op := range m.ops {
		last := pts[len(pts)-1]
		if last.state == StateEnded {
			break
		}
		b := budgetAt(op.At)
		switch op.Kind {
		case OpDisconnect:
			if last.state == StateActive {
				curCred = op.Credential
				pts = append(pts, nPoint{at: op.At, state: StatePaused, budget: b, gen: curGen, cred: curCred})
			}
		case OpResume:
			if last.state == StatePaused && op.Credential == curCred {
				curGen++
				curCred = ""
				pts = append(pts, nPoint{at: op.At, state: StateActive, budget: b, gen: curGen})
			}
		case OpEvent:
			if last.state != StateLocked && op.Seq >= int64(m.cfg.LockThreshold) {
				pts = append(pts, nPoint{at: op.At, state: StateLocked, budget: b, gen: curGen, cred: curCred})
			}
		case OpUnlock:
			if last.state == StateLocked {
				pts = append(pts, nPoint{at: op.At, state: StateActive, budget: b, gen: curGen, cred: curCred})
			}
		case OpSubmit:
			pts = append(pts, nPoint{at: op.At, state: StateEnded, end: op.Credential, budget: b, gen: curGen, cred: curCred})
		case OpExtend:
			// Extend 不改变状态；只切分进行中区间，暂停区间保持连续
			// （单次暂停上限必须以原始段起点衡量）。
			if last.state == StateActive || last.state == StateLocked {
				pts = append(pts, nPoint{at: op.At, state: last.state, budget: b, gen: curGen, cred: curCred})
			}
		}
	}
	return pts
}

// replay 从创建点逐区间重放到 t，计算作答时长、暂停时长与自动结束。
func (m *naiveModel) replay(t int64) stateAt {
	pts := m.buildPoints()
	st := stateAt{state: StateActive, endAt: -1, gen: 1, budget: m.cfg.Budget}
	var used, paused, pauseBudgetUsed int64
	endAt, endReason := int64(-1), ""
	type bh struct{ at, budget int64 }
	var hikes []bh
	cb0 := m.cfg.Budget
	for _, op := range m.ops {
		if op.Kind == OpExtend {
			cb0 += op.Seq
			hikes = append(hikes, bh{op.At, cb0})
		}
	}
	// 每个 active/locked 区间起点的作答时长：Extend 不切暂停段，
	// 但 active 段被 Extend 切分后，后续段起点的 used 必须在扫描时持续累计。
	for i := 0; i < len(pts); i++ {
		p := pts[i]
		st.gen, st.cred, st.budget = p.gen, p.cred, p.budget
		if endAt >= 0 {
			break
		}
		if p.state == StateEnded {
			endAt, endReason = p.at, p.end
			break
		}
		lo := p.at
		hi := t
		if i+1 < len(pts) && pts[i+1].at < hi {
			hi = pts[i+1].at
		}
		if hi < lo {
			hi = lo
		}
		st.state = p.state
		switch p.state {
		case StateActive, StateLocked:
			// active 段作答速率恒为 1；段内 Extend 以预算阶梯出现。
			stairs := []fStair{{lo, p.budget}}
			for _, h := range hikes {
				if h.at > lo && h.at <= hi {
					stairs = append(stairs, fStair{h.at, h.budget})
				}
			}
			uAt := func(x int64) int64 { return used + (x - lo) }
			hit := findBudgetHit(stairs, hi, lo, used, uAt)
			switch {
			case lo >= m.cfg.Deadline:
				endAt, endReason = lo, "deadline"
			case hit >= 0 && hit <= m.cfg.Deadline:
				used, endAt, endReason = uAt(hit), hit, "budget"
			case m.cfg.Deadline <= hi:
				used += m.cfg.Deadline - lo
				endAt, endReason = m.cfg.Deadline, "deadline"
			default:
				used += hi - lo
			}
		case StatePaused:
			remPause := m.cfg.PauseBudget - pauseBudgetUsed
			_ = remPause
			if remPause < 0 {
				remPause = 0
			}
			d := hi - lo
			singleHit := lo + m.cfg.MaxSinglePause
			// 暂停预算的全局余量（含本段之前所有暂停段）。
			remPauseGlobal := m.cfg.PauseBudget - pauseBudgetUsed
			if remPauseGlobal < 0 {
				remPauseGlobal = 0
			}
			stairs := []fStair{{lo, p.budget}}
			cb := p.budget
			for _, h := range hikes {
				if h.at > lo && h.at <= hi {
					cb = h.budget
					stairs = append(stairs, fStair{h.at, cb})
				}
			}
			// 计算每个阶梯起点处的 used（暂停预算点之前为常数 used，之后线性）。
			usedAt := func(x int64) int64 {
				dd := x - lo
				u := used
				if dd > remPauseGlobal {
					u += dd - remPauseGlobal
				}
				return u
			}
			budgetHit := findBudgetHit(stairs, hi, lo+remPauseGlobal, used, usedAt)
			hasBudget := budgetHit >= 0
			switch {
			case lo >= m.cfg.Deadline:
				endAt, endReason = lo, "deadline"
			case hasBudget && budgetHit <= singleHit && budgetHit <= m.cfg.Deadline:
				paused += budgetHit - lo
				used, endAt, endReason = usedAt(budgetHit), budgetHit, "budget"
			case singleHit < hi && singleHit <= m.cfg.Deadline:
				paused += singleHit - lo
				used = usedAt(singleHit)
				endAt, endReason = singleHit, "single_pause_limit"
			case m.cfg.Deadline <= hi:
				paused += m.cfg.Deadline - lo
				endAt, endReason = m.cfg.Deadline, "deadline"
			default:
				paused += d
				pauseBudgetUsed += d
				if d > remPauseGlobal {
					used += d - remPauseGlobal
				}
			}
		}
	}
	st.used, st.paused = used, paused
	if endAt >= 0 {
		st.state, st.endAt, st.endReason = StateEnded, endAt, endReason
	} else {
		st.endAt = 0
	}
	return st
}

// windowCount 全量扫描 (now-WindowLen, now] 内事件数。
func (m *naiveModel) windowCount(now int64) int {
	cutoff := now - m.cfg.WindowLen
	c := 0
	for _, ev := range m.events {
		if ev.at > cutoff && ev.at <= now {
			c++
		}
	}
	return c
}

func (m *naiveModel) disconnect(now int64) (string, *Error) {
	st := m.replay(now)
	if st.state == StateEnded {
		return "", m.endedErr()
	}
	if st.state != StateActive {
		return "", errf(ErrStateNotAllowed, SubNone, "disconnect requires active state")
	}
	cred := m.issueCred()
	m.curCred = cred
	m.ops = append(m.ops, AcceptedOp{Kind: OpDisconnect, At: now, Credential: cred, Generation: st.gen})
	return cred, nil
}

func (m *naiveModel) resume(now int64, cred string) (int64, *Error) {
	st := m.replay(now)
	if st.state == StateEnded {
		return 0, m.endedErr()
	}
	if m.curCred == "" {
		return 0, errf(ErrCredential, SubUnknownCredential, "no outstanding credential")
	}
	if cred != m.curCred {
		return 0, errf(ErrCredential, SubStaleCredential, "stale credential")
	}
	if st.state != StatePaused {
		return 0, errf(ErrStateNotAllowed, SubNone, "resume requires paused state")
	}
	m.gen = st.gen + 1
	m.curCred = ""
	m.ops = append(m.ops, AcceptedOp{Kind: OpResume, At: now, Credential: cred, Generation: m.gen})
	return m.gen, nil
}

func (m *naiveModel) answer(now, gen int64, a Answer) *Error {
	st := m.replay(now)
	if st.state == StateEnded {
		return m.endedErr()
	}
	if gen != st.gen {
		return errf(ErrCredential, SubOldGeneration, "old generation")
	}
	if st.state == StatePaused {
		if a.Seq > m.maxSeq {
			return errf(ErrStateNotAllowed, SubNone, "answer emitted after pause began")
		}
	} else if st.state != StateActive {
		return errf(ErrStateNotAllowed, SubNone, "answer not allowed")
	}
	if q, ok := m.seqOwner[a.Seq]; ok {
		if q != a.Question {
			return errf(ErrAnswerLateOrDuplicate, SubDuplicateAnswer, "seq reused by %q", q)
		}
		return errf(ErrAnswerLateOrDuplicate, SubDuplicateAnswer, "duplicate seq")
	}
	if st.state != StatePaused && a.Seq < m.maxSeq {
		return errf(ErrAnswerLateOrDuplicate, SubLateAnswer, "late answer")
	}
	m.seqOwner[a.Seq] = a.Question
	if a.Seq > m.maxSeq {
		m.maxSeq = a.Seq
	}
	if cur, ok := m.answers[a.Question]; !ok || a.Seq > cur.Seq {
		m.answers[a.Question] = a
	}
	m.ops = append(m.ops, AcceptedOp{Kind: OpAnswer, At: now, Seq: a.Seq, Generation: gen, Answer: a})
	return nil
}

func (m *naiveModel) event(now int64, rank int64, kind string) *Error {
	st := m.replay(now)
	if st.state == StateEnded {
		return m.endedErr()
	}
	pre := m.windowCount(now)
	cnt := pre + 1
	m.events = append(m.events, nEvent{at: now, rank: rank, kind: kind})
	lockedBefore := st.state == StateLocked
	m.ops = append(m.ops, AcceptedOp{Kind: OpEvent, At: now, Seq: int64(cnt),
		Generation: st.gen, Answer: Answer{Text: kind}})
	if !lockedBefore {
		if cnt >= m.cfg.LockThreshold {
			m.locked = true
		}
	}
	armed := pre < m.cfg.WarnThreshold
	if armed && cnt >= m.cfg.WarnThreshold {
		m.warnings++
		if m.warnings >= m.cfg.MaxWarnings {
			m.violation = true
			m.locked = false
			m.ops = append(m.ops, AcceptedOp{Kind: OpSubmit, At: now, Credential: "warning_limit"})
		}
	}
	return nil
}

func (m *naiveModel) unlock(now int64) *Error {
	st := m.replay(now)
	if st.state == StateEnded {
		return m.endedErr()
	}
	if st.state != StateLocked {
		return errf(ErrStateNotAllowed, SubNone, "unlock requires locked state")
	}
	m.locked = false
	m.ops = append(m.ops, AcceptedOp{Kind: OpUnlock, At: now, Generation: st.gen})
	return nil
}

func (m *naiveModel) submit(now int64) *Error {
	st := m.replay(now)
	if st.state == StateEnded {
		return m.endedErr()
	}
	m.ops = append(m.ops, AcceptedOp{Kind: OpSubmit, At: now, Credential: "submit", Generation: st.gen})
	return nil
}

func (m *naiveModel) extend(now, delta int64) *Error {
	st := m.replay(now)
	if st.state == StateEnded {
		return m.endedErr()
	}
	m.budget = st.budget + delta
	m.ops = append(m.ops, AcceptedOp{Kind: OpExtend, At: now, Seq: delta, Generation: st.gen})
	return nil
}

func (m *naiveModel) snapshot(now int64) Snapshot {
	st := m.replay(now)
	answers := make(map[string]Answer, len(m.answers))
	for q, a := range m.answers {
		answers[q] = a
	}
	if st.endReason == "warning_limit" {
		m.violation = true
	}
	return Snapshot{
		State:       st.state,
		Answers:     answers,
		UsedActive:  st.used,
		TotalPaused: st.paused,
		Warnings:    m.warnings,
		Locked:      st.state == StateLocked,
		Violation:   st.endReason == "warning_limit",
		Settled:     st.state == StateEnded,
		EndAt:       st.endAt,
		EndReason:   st.endReason,
		LastSeq:     m.maxSeq,
		WindowCount: m.windowCount(now),
		Generation:  st.gen,
	}
}
