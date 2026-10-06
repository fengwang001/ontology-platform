package exam

// eventRecord 记录一条异常事件，客户端时刻仅留存不参与判定。
type eventRecord struct {
	Kind       EventKind
	ClientTime int64
	Arrival    int64
}

// session 保存单个考试会话的全部状态。
//
// 时长账目：作答时间 = 进行中(active/locked)区间长度之和
// + max(0, 暂停总时长-暂停预算)。所有量均由区间端点惰性求值，
// 会话本身不维护任何随时间推进的时钟。
type session struct {
	id       string
	status   Status
	start    int64
	budget   int64
	deadline int64

	activeBase    int64 // 当前进行区间之前已累计的进行时长
	intervalStart int64 // 当前进行区间(active/locked)的起点
	pausedBase    int64 // 当前暂停之前已累计的暂停总时长
	pauseStart    int64 // 当前暂停的起点

	generation int64
	token      string // 最近一次暂停签发的续考凭证，续考成功后清空
	waterline  int64  // 暂停时记录的序号水位
	maxSeenSeq int64  // 会话内已接受作答的最大序号

	maxSeq map[string]int64        // 每题已落定作答的最大序号
	landed map[string]LandedAnswer // 每题最终作答

	window   []int64 // 滑动窗口内事件的服务端到达时刻，队首最旧
	whead    int     // 窗口队首下标，避免频繁搬移
	eventLog []eventRecord
	warnings int

	settlement *Settlement
}

func newSession(id string, now, deadline, budget int64) *session {
	return &session{
		id:            id,
		status:        StatusActive,
		start:         now,
		budget:        budget,
		deadline:      deadline,
		intervalStart: now,
		generation:    1,
		waterline:     -1,
		maxSeenSeq:    -1,
		maxSeq:        make(map[string]int64),
		landed:        make(map[string]LandedAnswer),
	}
}

// pauseTotalAt 返回 t 时刻的暂停总时长。
func (s *session) pauseTotalAt(t int64) int64 {
	total := s.pausedBase
	if s.status == StatusPaused {
		total += t - s.pauseStart
	}
	return total
}

// usedAt 返回 t 时刻的有效作答时长。
func (s *session) usedAt(t int64, cfg *Config) int64 {
	active := s.activeBase
	if s.status == StatusActive || s.status == StatusLocked {
		active += t - s.intervalStart
	}
	excess := s.pauseTotalAt(t) - cfg.PauseBudget
	if excess < 0 {
		excess = 0
	}
	return active + excess
}

// autoEndAt 计算会话必须自动结束的最早时刻及其原因。
// 作答预算与绝对截止恰等于视为到达；单次暂停须严格超过上限才结束。
func (s *session) autoEndAt(cfg *Config) (int64, EndReason, bool) {
	if s.status == StatusEnded {
		return 0, EndReasonNone, false
	}
	best, reason := s.deadline, EndReasonDeadline

	var exhaust int64
	if s.status == StatusPaused {
		// 暂停中作答时间只在暂停预算用尽后累计。
		exhaust = s.pauseStart + (s.budget - s.activeBase) + cfg.PauseBudget - s.pausedBase
	} else {
		excess := s.pausedBase - cfg.PauseBudget
		if excess < 0 {
			excess = 0
		}
		exhaust = s.intervalStart + (s.budget - s.activeBase) - excess
	}
	if exhaust <= best {
		best, reason = exhaust, EndReasonTimeExhausted
	}

	if s.status == StatusPaused {
		if limit := s.pauseStart + cfg.SinglePauseMax + 1; limit < best {
			best, reason = limit, EndReasonPauseLimit
		}
	}
	return best, reason, true
}

// maybeEnd 在下一次触及会话时惰性落地自动结束，落地时刻取应结束时刻。
func (s *session) maybeEnd(now int64, cfg *Config) {
	t, reason, ok := s.autoEndAt(cfg)
	if !ok || now < t {
		return
	}
	s.settle(t, reason, false, cfg)
}

// settle 完成结算，结算结果不可变。
func (s *session) settle(end int64, reason EndReason, violation bool, cfg *Config) {
	answers := make(map[string]LandedAnswer, len(s.landed))
	for q, a := range s.landed {
		answers[q] = a
	}
	s.settlement = &Settlement{
		Answers:             answers,
		EffectiveAnswerTime: s.usedAt(end, cfg),
		TotalPauseTime:      s.pauseTotalAt(end),
		Warnings:            s.warnings,
		Violation:           violation,
		EndAt:               end,
		Reason:              reason,
	}
	s.status = StatusEnded
}

// pause 由进行中转为暂停，签发凭证并记录序号水位。
func (s *session) pause(now int64, token string) {
	s.activeBase += now - s.intervalStart
	s.status = StatusPaused
	s.pauseStart = now
	s.token = token
	s.waterline = s.maxSeenSeq
}

// resume 续考：暂停区间入账，签发新代次，旧凭证失效。
func (s *session) resume(now int64) {
	s.pausedBase += now - s.pauseStart
	s.status = StatusActive
	s.intervalStart = now
	s.token = ""
	s.waterline = -1
	s.generation++
}

// lock 锁定会话。锁定期间作答时间照常累计；若此前处于暂停，
// 暂停区间在此结算，锁定按进行区间计时。窗口内事件不清空。
func (s *session) lock(now int64) {
	if s.status == StatusPaused {
		s.pausedBase += now - s.pauseStart
		s.intervalStart = now
	}
	s.status = StatusLocked
}

// unlock 由监考解除锁定，回到进行中；不清空窗口内事件。
func (s *session) unlock() {
	s.status = StatusActive
}

// land 落定一条作答。调用方已完成代次与状态检查。
func (s *session) land(a Answer) error {
	cur, ok := s.maxSeq[a.QuestionID]
	if ok {
		if a.Seq < cur {
			return ErrLateAnswer
		}
		if a.Seq == cur {
			return ErrDuplicateAnswer
		}
	}
	s.maxSeq[a.QuestionID] = a.Seq
	s.landed[a.QuestionID] = LandedAnswer{Seq: a.Seq, Payload: a.Payload, ClientTime: a.ClientTime}
	if a.Seq > s.maxSeenSeq {
		s.maxSeenSeq = a.Seq
	}
	return nil
}

// addEvent 按服务端到达时刻把事件计入滑动窗口（左开右闭），
// 并按阈值触发警告、锁定或违规结束。均摊 O(1)。
func (s *session) addEvent(now int64, kind EventKind, clientTime int64, cfg *Config) {
	s.eventLog = append(s.eventLog, eventRecord{Kind: kind, ClientTime: clientTime, Arrival: now})
	s.window = append(s.window, now)
	cut := now - cfg.Window
	for s.whead < len(s.window) && s.window[s.whead] <= cut {
		s.whead++
	}
	if s.whead >= 64 && s.whead*2 >= len(s.window) {
		s.window = append([]int64(nil), s.window[s.whead:]...)
		s.whead = 0
	}
	count := len(s.window) - s.whead
	if count >= cfg.WarnThreshold {
		s.warnings++
	}
	if count >= cfg.LockThreshold && s.status != StatusLocked {
		s.lock(now)
	}
	if s.warnings >= cfg.WarnLimit {
		s.settle(now, EndReasonViolation, true, cfg)
	}
}
