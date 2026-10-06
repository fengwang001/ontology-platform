package ontology

import (
	"strconv"
	"sync"
)

// State 表示会话生命周期状态。
type State int

const (
	StateActive State = iota + 1
	StatePaused
	StateLocked
	StateEnded
)

// Config 是会话级配置（均为整数时刻/时长/次数）。
type Config struct {
	Budget         int64 // 作答时长预算（有效作答时长）
	Deadline       int64 // 绝对截止时刻（服务端时钟）
	PauseBudget    int64 // 暂停总时长预算
	MaxSinglePause int64 // 单次暂停上限（恰等于允许）
	WindowLen      int64 // 滑动窗口长度；窗口为左开右闭 (t-WindowLen, t]
	WarnThreshold  int   // 窗口内事件警告阈值（> 0）
	LockThreshold  int   // 窗口内事件锁定阈值（>= WarnThreshold）
	MaxWarnings    int   // 累计警告次数上限，达到即违规结束（> 0）
}

// Answer 是一条考生作答记录。
type Answer struct {
	Question string
	Seq      int64 // 会话内全局单调序号（不要求连续）
	Text     string
}

// Snapshot 是可在引擎与朴素模型之间逐项对照的结算/观测快照。
type Snapshot struct {
	State       State
	Answers     map[string]Answer // 每题序号最大的已接受作答
	UsedActive  int64             // 有效作答时长
	TotalPaused int64             // 暂停挂钟总时长
	Warnings    int
	Locked      bool
	Violation   bool
	Settled     bool
	EndAt       int64 // 结束落地时刻；未结束为 0
	EndReason   string
	LastSeq     int64 // 会话内已接受作答的最大序号
	WindowCount int   // 当前窗口惰性清理后的事件数
	Generation  int64
}

// event 是窗口内一条异常事件；同刻多条按上报顺序保序。
type event struct {
	at   int64
	rank int64
}

type budgetHike struct {
	at, budget int64
}

// session 保存单场考试的全部账目。时间账用“冻结账目 + 冻结点”表示，
// 任意时刻的有效作答时长都在 O(1) 内求出。
type session struct {
	cfg Config

	state      State
	generation int64

	frozenAt   int64 // 账目冻结时刻（最后一次状态迁移/结束时刻）
	usedActive int64 // 截至 frozenAt 的有效作答时长
	totalPause int64 // 截至 frozenAt 的暂停挂钟总时长

	pauseStart           int64 // 当前暂停段起点（仅 Paused）
	pauseBudgetRem       int64 // 进入本次暂停时剩余的暂停预算
	pauseSegStart        int64 // 本次暂停段的原始起点（fold 不移动，用于单次上限）
	usedAtSegStart       int64 // 本次暂停段开始时的 usedActive
	totalPauseAtSegStart int64
	budgetAtSegStart     int64
	budgetHikes          []budgetHike

	answers  map[string]Answer // 每题当前最终作答
	seqOwner map[int64]string  // 已接受序号 -> 题目，O(1) 重复判定
	maxSeq   int64             // 已接受作答的最大序号

	win []event // 按到达时刻单调的滑动窗口队列

	warnings  int
	warnArmed bool // 当前 active 段警告边沿是否仍可触发
	locked    bool // 当前是否处于监考锁定

	lastCred string // 最近一次暂停凭证；已续考或无暂停时为空

	endAt     int64
	endReason string
	violation bool
	settled   bool

	log []AcceptedOp // 供朴素模型回放的已接受操作流水
}

// Engine 是并发安全的考试会话引擎；所有方法等价于某个全局串行顺序。
type Engine struct {
	mu       sync.Mutex
	clock    int64 // 服务端单调时钟，只被接受的操作推进
	eventNo  int64
	credNo   int64
	sessions map[string]*session
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{sessions: map[string]*session{}}
}

// CreateSession 在 now 时刻创建会话。
func (e *Engine) CreateSession(id string, now int64, cfg Config) error {
	if id == "" || cfg.Budget < 0 || cfg.Deadline < now || cfg.PauseBudget < 0 ||
		cfg.MaxSinglePause < 0 || cfg.WindowLen < 0 ||
		cfg.WarnThreshold <= 0 || cfg.LockThreshold < cfg.WarnThreshold || cfg.MaxWarnings <= 0 {
		return errf(ErrInvalidParam, SubNone, "invalid session config")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.advanceClock(now); err != nil {
		return err
	}
	if _, ok := e.sessions[id]; ok {
		return errf(ErrInvalidParam, SubNone, "session id exists")
	}
	s := &session{
		cfg:        cfg,
		state:      StateActive,
		generation: 1,
		frozenAt:   now,
		answers:    map[string]Answer{},
		seqOwner:   map[int64]string{},
		warnArmed:  true,
	}
	e.sessions[id] = s
	// 创建时预算为 0 或 now==deadline：惰性落地时刻为 now。
	s.touch(now, e)
	return nil
}

// advanceClock 校验并推进服务端时钟；回退返回 ErrClockRegression 且不改状态。
// 调用方须持有 e.mu。
func (e *Engine) advanceClock(now int64) *Error {
	if now < e.clock {
		return errf(ErrClockRegression, SubNone, "clock %d < last %d", now, e.clock)
	}
	e.clock = now
	return nil
}

// get 校验时钟、存在性，并惰性落地自动结束。调用方须持有 e.mu。
func (e *Engine) get(id string, now int64) (*session, *Error) {
	if err := e.advanceClock(now); err != nil {
		return nil, err
	}
	s, ok := e.sessions[id]
	if !ok {
		return nil, errf(ErrSessionNotFound, SubNone, "session %q", id)
	}
	s.touch(now, e)
	return s, nil
}

// touch 把 [frozenAt, now] 区间按当前状态记账，并在预算耗尽/到达截止/
// 单次暂停超限时惰性落地自动结束；落地时刻取触发时刻，而非触及时刻 now。
// 恰等于阈值视为到达。调用方须持有 e.mu。
func (s *session) touch(now int64, e *Engine) {
	if s.state == StateEnded {
		return
	}
	switch s.state {
	case StateActive, StateLocked:
		elapsed := now - s.frozenAt
		rem := s.cfg.Budget - s.usedActive
		budgetHit := s.frozenAt + rem
		hasBudget := rem <= elapsed && rem >= 0
		switch {
		case s.frozenAt >= s.cfg.Deadline:
			s.fold(s.frozenAt)
			s.finish(s.frozenAt, "deadline", false)
		case hasBudget && budgetHit <= s.cfg.Deadline:
			s.fold(budgetHit)
			s.finish(budgetHit, "budget", false)
		case s.cfg.Deadline <= now:
			s.fold(s.cfg.Deadline)
			s.finish(s.cfg.Deadline, "deadline", false)
		default:
			s.fold(now)
		}
	case StatePaused:
		// 三个可能的自动结束时刻，取最先到者（恰等于视为到达）。
		segDur := now - s.pauseSegStart
		singleHit := s.pauseSegStart + s.cfg.MaxSinglePause
		// 段内 Extend 只增预算：budgetHikes 记录暂停段内的预算阶梯。
		// 有效作答时长在段起点为 usedAtSegStart，挂钟越过剩余暂停预算后
		// 速率变为 1；逐阶梯求首次达到当时预算的挂钟时刻。
		var fstairs []fStair
		fstairs = append(fstairs, fStair{at: s.pauseSegStart, budget: s.budgetAtSegStart})
		for _, h := range s.budgetHikes {
			if h.at > s.pauseSegStart && h.at <= now {
				fstairs = append(fstairs, fStair{at: h.at, budget: h.budget})
			}
		}
		usedAt := func(x int64) int64 {
			dd := x - s.pauseSegStart
			u := s.usedAtSegStart
			if dd > s.pauseBudgetRem {
				u += dd - s.pauseBudgetRem
			}
			return u
		}
		pausePoint := s.pauseSegStart + s.pauseBudgetRem
		budgetHit := findBudgetHit(fstairs, now, pausePoint, s.usedAtSegStart, usedAt)
		hit, reason := earliestEnd(s.cfg.Deadline <= now, s.cfg.Deadline, "deadline",
			budgetHit >= 0 && budgetHit <= now, budgetHit, "budget",
			segDur > s.cfg.MaxSinglePause, singleHit, "single_pause_limit")
		if reason != "" {
			s.fold(hit)
			s.finish(hit, reason, false)
			return
		}
		s.fold(now)
	}
}

// earliestEnd 在已触发的候选结束时刻中取最小者。
func earliestEnd(hits ...any) (int64, string) {
	best := int64(1 << 62)
	reason := ""
	for i := 0; i < len(hits); i += 3 {
		if !hits[i].(bool) {
			continue
		}
		at := hits[i+1].(int64)
		r := hits[i+2].(string)
		if reason == "" || at < best {
			best, reason = at, r
		}
	}
	return best, reason
}

// fold 把账目从 frozenAt 推进到 t（t 不晚于任何自动结束触发时刻）。
func (s *session) fold(t int64) {
	if t <= s.frozenAt {
		return
	}
	switch s.state {
	case StateActive, StateLocked:
		s.usedActive += t - s.frozenAt
	case StatePaused:
		// 以原始段起点计算：超出暂停预算的部分计作答。
		segDur := t - s.pauseSegStart
		// pauseBudgetRem 是扣除本段之前所有暂停后的总预算余量。
		s.totalPause = s.totalPauseAtSegStart + segDur
		s.usedActive = s.usedAtSegStart + max0(segDur-s.pauseBudgetRem)
	}
	s.frozenAt = t
}

// finish 把会话定型为已结束并立即结算（结算不可变）。
func (s *session) finish(at int64, reason string, violation bool) {
	s.state = StateEnded
	s.endAt = at
	s.endReason = reason
	s.violation = violation
	s.locked = false
	s.lastCred = ""
	s.warnArmed = false
	s.settled = true
}

func max0(x int64) int64 {
	if x > 0 {
		return x
	}
	return 0
}

// Disconnect 在 now 时刻上报断线，会话转为暂停并签发续考凭证。
func (e *Engine) Disconnect(id string, now int64) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return "", gerr
	}
	if s.state == StateEnded {
		return "", errf(ErrSessionEnded, SubSettled, "session ended")
	}
	if s.state != StateActive {
		return "", errf(ErrStateNotAllowed, SubNone, "disconnect requires active state, got %d", s.state)
	}
	s.state = StatePaused
	s.pauseStart = now
	s.pauseSegStart = now
	s.usedAtSegStart = s.usedActive
	s.totalPauseAtSegStart = s.totalPause
	s.budgetAtSegStart = s.cfg.Budget
	s.budgetHikes = nil
	s.pauseBudgetRem = s.cfg.PauseBudget - s.totalPause
	if s.pauseBudgetRem < 0 {
		s.pauseBudgetRem = 0
	}
	e.credNo++
	s.lastCred = "cred-" + strconv.FormatInt(e.credNo, 10)
	s.appendLog(OpDisconnect, now, 0, s.generation, s.lastCred, Answer{})
	return s.lastCred, nil
}

// Resume 出示最近一次暂停的凭证续考，返回新的会话代次。
// 旧凭证返回 ErrCredential/StaleCredential；无法识别返回 UnknownCredential。
func (e *Engine) Resume(id string, now int64, cred string) (int64, error) {
	if cred == "" {
		return 0, errf(ErrInvalidParam, SubNone, "empty credential")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return 0, gerr
	}
	if s.state == StateEnded {
		return 0, errf(ErrSessionEnded, SubSettled, "session ended")
	}
	// 凭证判定优先于状态判定；旧凭证与未知凭证可区分。
	if s.lastCred == "" {
		return 0, errf(ErrCredential, SubUnknownCredential, "no outstanding pause credential")
	}
	if cred != s.lastCred {
		return 0, errf(ErrCredential, SubStaleCredential, "credential is not the latest")
	}
	if s.state != StatePaused {
		return 0, errf(ErrStateNotAllowed, SubNone, "resume requires paused state, got %d", s.state)
	}
	s.state = StateActive
	s.generation++
	s.lastCred = ""
	s.warnArmed = true
	gen := s.generation
	s.appendLog(OpResume, now, 0, gen, cred, Answer{})
	return gen, nil
}

// SubmitAnswer 接收一条带序号的作答。
// 重复序号返回 DuplicateAnswer；低于落定水位返回 LateAnswer；
// 旧代次返回 ErrCredential/OldGeneration。
func (e *Engine) SubmitAnswer(id string, now, generation int64, a Answer) error {
	if a.Question == "" || a.Seq <= 0 || generation <= 0 {
		return errf(ErrInvalidParam, SubNone, "invalid answer or generation")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return gerr
	}
	if s.state == StateEnded {
		return errf(ErrSessionEnded, SubSettled, "session ended")
	}
	if generation != s.generation {
		return errf(ErrCredential, SubOldGeneration, "gen %d != current %d", generation, s.generation)
	}
	if s.state == StatePaused {
		// 暂停前已发出、暂停后才到达：以序号判定归属。
		// 水位 = 暂停时已落定的最大序号 + 1，seq < 水位即在途作答。
		if a.Seq > s.maxSeq {
			return errf(ErrStateNotAllowed, SubNone, "answer emitted after pause began")
		}
	} else if s.state != StateActive {
		// Locked 等非作答状态。
		return errf(ErrStateNotAllowed, SubNone, "answer not allowed in state %d", s.state)
	}
	if q, dup := s.seqOwner[a.Seq]; dup {
		// 重复提交：不改变任何状态。
		if q != a.Question {
			return errf(ErrAnswerLateOrDuplicate, SubDuplicateAnswer, "seq %d already used by %q", a.Seq, q)
		}
		return errf(ErrAnswerLateOrDuplicate, SubDuplicateAnswer, "duplicate seq %d", a.Seq)
	}
	if s.state != StatePaused && a.Seq < s.maxSeq {
		// 迟到：序号低于已落定作答。
		return errf(ErrAnswerLateOrDuplicate, SubLateAnswer, "seq %d < settled %d", a.Seq, s.maxSeq)
	}
	s.seqOwner[a.Seq] = a.Question
	if a.Seq > s.maxSeq {
		s.maxSeq = a.Seq
	}
	if cur, ok := s.answers[a.Question]; !ok || a.Seq > cur.Seq {
		s.answers[a.Question] = a
	}
	s.appendLog(OpAnswer, now, a.Seq, generation, "", a)
	return nil
}

// evictWindow 按左开右闭区间 (now-WindowLen, now] 清理过期事件。
// 队列按到达时刻单调，过期项恒在队首，单条事件至多入队/出队一次，
// 因此每条事件的均摊处理开销为 O(1)，与历史事件总数无关。
func (s *session) evictWindow(now int64) {
	cutoff := now - s.cfg.WindowLen // 仅保留 at > cutoff
	for len(s.win) > 0 && s.win[0].at <= cutoff {
		s.win = s.win[1:]
	}
	if len(s.win) == 0 {
		s.win = s.win[:0]
	}
	// 计数回落到阈值以下后，允许下一次越阈再次触发警告。
	if len(s.win) < s.cfg.WarnThreshold {
		s.warnArmed = true
	}
}

// RecordEvent 在 now 时刻记录一条离开页面/切换窗口等异常事件。
// 同一时刻多条事件须按上报顺序逐条调用，rank 由此保序。
func (e *Engine) RecordEvent(id string, now int64, kind string) error {
	if kind == "" {
		return errf(ErrInvalidParam, SubNone, "empty event kind")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return gerr
	}
	if s.state == StateEnded {
		return errf(ErrSessionEnded, SubSettled, "session ended")
	}
	s.evictWindow(now)
	e.eventNo++
	s.win = append(s.win, event{at: now, rank: e.eventNo})
	cnt := len(s.win)
	s.appendLog(OpEvent, now, int64(cnt), s.generation, "", Answer{Text: kind})
	// 警告与锁定均为插入后一次性边沿触发；同刻多条逐条判定。
	// 先迁移到锁定（锁定中作答时间照常累计），再判断警告上限违规结束。
	if s.state != StateLocked && cnt >= s.cfg.LockThreshold {
		s.fold(now)
		s.state = StateLocked
		s.locked = true
		s.frozenAt = now
	}
	if s.warnArmed && cnt >= s.cfg.WarnThreshold {
		s.warnings++
		s.warnArmed = false
		if s.warnings >= s.cfg.MaxWarnings {
			s.fold(now)
			s.finish(now, "warning_limit", true)
			return nil
		}
	}
	return nil
}

// Unlock 由监考解除锁定；不清空窗口内事件。
func (e *Engine) Unlock(id string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return gerr
	}
	if s.state == StateEnded {
		return errf(ErrSessionEnded, SubSettled, "session ended")
	}
	if s.state != StateLocked {
		return errf(ErrStateNotAllowed, SubNone, "unlock requires locked state, got %d", s.state)
	}
	s.state = StateActive
	s.locked = false
	s.warnArmed = true // 残留事件可能在下一条事件到达时再次越阈
	s.appendLog(OpUnlock, now, 0, s.generation, "", Answer{})
	return nil
}

// Submit 由考生主动提交，立即结算。重复提交返回 ErrSessionEnded/Settled。
func (e *Engine) Submit(id string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return gerr
	}
	if s.state == StateEnded {
		return errf(ErrSessionEnded, SubSettled, "already submitted/settled")
	}
	s.fold(now)
	s.finish(now, "submit", false)
	s.appendLog(OpSubmit, now, 0, s.generation, "", Answer{})
	return nil
}

// Extend 由监考延长作答预算 delta；只对未结束会话有效，不改变绝对截止。
func (e *Engine) Extend(id string, now, delta int64) error {
	if delta <= 0 {
		return errf(ErrInvalidParam, SubNone, "extend delta must be positive")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return gerr
	}
	if s.state == StateEnded {
		return errf(ErrSessionEnded, SubSettled, "session ended")
	}
	s.cfg.Budget += delta // Deadline 保持不变
	if s.state == StatePaused {
		s.budgetHikes = append(s.budgetHikes, budgetHike{at: now, budget: s.cfg.Budget})
	}
	s.appendLog(OpExtend, now, delta, s.generation, "", Answer{})
	return nil
}

// Snapshot 返回 now 时刻的观测/结算快照（触发惰性落地但不改变语义状态）。
func (e *Engine) Snapshot(id string, now int64) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, gerr := e.get(id, now)
	if gerr != nil {
		return Snapshot{}, gerr
	}
	s.evictWindow(now)
	return s.snapshot(now), nil
}

func (s *session) snapshot(now int64) Snapshot {
	answers := make(map[string]Answer, len(s.answers))
	for q, a := range s.answers {
		answers[q] = a
	}
	snap := Snapshot{
		State:       s.state,
		Answers:     answers,
		UsedActive:  s.usedActive,
		TotalPaused: s.totalPause,
		Warnings:    s.warnings,
		Locked:      s.locked,
		Violation:   s.violation,
		Settled:     s.settled,
		EndAt:       s.endAt,
		EndReason:   s.endReason,
		LastSeq:     s.maxSeq,
		WindowCount: len(s.win),
		Generation:  s.generation,
	}
	if s.state != StateEnded {
		// 观测时刻的瞬时账（不落地）：与 touch/fold 同口径重算。
		switch s.state {
		case StateActive, StateLocked:
			used := s.usedActive + (now - s.frozenAt)
			deadlineAt := s.cfg.Deadline
			if now > deadlineAt {
				used = s.usedActive + (deadlineAt - s.frozenAt)
			}
			if used > s.cfg.Budget {
				used = s.cfg.Budget
			}
			snap.UsedActive = used
		case StatePaused:
			segDur := now - s.pauseSegStart
			snap.TotalPaused = s.totalPauseAtSegStart + segDur
			extra := max0(segDur - s.pauseBudgetRem)
			used := s.usedAtSegStart + extra
			if used > s.cfg.Budget {
				used = s.cfg.Budget
			}
			snap.UsedActive = used
		}
	}
	return snap
}
