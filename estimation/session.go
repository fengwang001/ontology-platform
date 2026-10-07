package estimation

import "sync"

// Session 为一次团队估点投票会话。所有方法可并发调用，内部以
// 单互斥锁串行化，效果等价于某个串行顺序；相同操作序列重放得到
// 完全相同的结果。
//
// 每个操作遵循统一的校验流水线，任一环节被拒即返回且只报第一个
// 错误，被拒绝的操作不改变任何状态与时钟：
//
//	参数非法 -> 时钟回退 -> [到期处理] -> 调用者不在会话 ->
//	权限不足 -> 状态不允许 -> 无人投票
//
// 通过参数与时钟检查的操作，即使随后被拒，也会先完成到期处理
// （在到期时刻揭示），这是规范明确要求的副作用。
type Session struct {
	mu         sync.Mutex
	deck       Deck
	maxRounds  int
	roundTTL   int64
	autoReveal bool
	host       string

	lastNow int64
	members map[string]Role
	voters  int // 在室投票者人数

	phase      Phase
	round      int
	roundStart int64
	box        ballotBox
}

// NewSession 创建会话。host 为主持人（创建者），不自动成为成员，
// 需通过 Join 加入后才能投票。时钟从 0 开始。
func NewSession(host string, deck []int, maxRounds int, roundTTL int64, autoReveal bool) (*Session, *Error) {
	if host == "" {
		return nil, newError(ErrInvalidParam, "主持人不能为空")
	}
	d, err := NewDeck(deck)
	if err != nil {
		return nil, err
	}
	if maxRounds < MinRounds || maxRounds > MaxRounds {
		return nil, newError(ErrInvalidParam, "轮次上限须在 1 到 5 之间")
	}
	if roundTTL < MinRoundTTL || roundTTL > MaxRoundTTL {
		return nil, newError(ErrInvalidParam, "单轮时限须在 1 到 86400 秒之间")
	}
	return &Session{
		deck:       d,
		maxRounds:  maxRounds,
		roundTTL:   roundTTL,
		autoReveal: autoReveal,
		host:       host,
		members:    map[string]Role{},
		box:        newBallotBox(),
	}, nil
}

// validNow 报告 now 是否在合法域内。
func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// checkClock 校验时钟回退。通过时钟检查后调用 expireIfDue。
func (s *Session) checkClock(now int64) *Error {
	if now < s.lastNow {
		return newError(ErrClockRegression, "now 小于上一次被接受操作的 now")
	}
	return nil
}

// expireIfDue 实现惰性到期：若处于投票阶段且 now 已到达或超过
// 到期时刻（开始时刻+T，恰等于视为已到期），则在到期时刻揭示。
// 此时无人投票按无有效票处理。
func (s *Session) expireIfDue(now int64) *RevealOutcome {
	if s.phase == PhaseVoting && now >= s.roundStart+s.roundTTL {
		return s.revealLocked(s.roundStart+s.roundTTL, TriggerExpired)
	}
	return nil
}

// autoRevealLocked 在自动揭示开启且“在室投票者不少于 1 且全体
// 在室投票者都已投票”时立即揭示。O(1) 判断。
func (s *Session) autoRevealLocked(now int64) *RevealOutcome {
	if s.autoReveal && s.phase == PhaseVoting && s.voters >= 1 && s.box.votedCount() == s.voters {
		return s.revealLocked(now, TriggerAuto)
	}
	return nil
}

// revealLocked 在当前轮揭示并推进状态机。调用前须保证处于投票阶段。
func (s *Session) revealLocked(at int64, trig RevealTrigger) *RevealOutcome {
	out := s.deck.evaluate(&s.box, s.round, s.maxRounds, at, trig)
	if out.IssueEnded {
		s.phase = PhaseIdle
	} else {
		s.phase = PhaseRevealed
	}
	return out
}

// isMember 报告用户是否为在室成员（主持人身份不隐含成员身份）。
func (s *Session) isMember(user string) bool {
	_, ok := s.members[user]
	return ok
}

// Join 以指定角色加入会话。已在会话内报已存在。投票阶段加入的
// 投票者立即计入全体；揭示后加入不影响已揭示的结果。
func (s *Session) Join(user string, role Role, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !role.Valid() || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空、角色非法或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	if s.isMember(user) {
		return exp, newError(ErrAlreadyExists, "用户已在会话内")
	}
	s.members[user] = role
	if role == RoleVoter {
		s.voters++
	}
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Leave 使用户离开会话。离开的投票者其票被撤销且不再计入全体，
// 可能触发自动揭示。主持人离开仅失去成员身份，仍保留主持人权限。
func (s *Session) Leave(user string, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	role, ok := s.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "用户不在会话内")
	}
	if role == RoleVoter {
		s.voters--
		s.box.revoke(user)
	}
	delete(s.members, user)
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// SetRole 修改成员角色。成员可修改自己，主持人可修改任何人。
// 投票者变为观察者时其票被撤销，可能触发自动揭示；观察者变为
// 投票者立即计入全体。揭示后改角色不影响已揭示的结果。
func (s *Session) SetRole(actor, target string, role Role, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor == "" || target == "" || !role.Valid() || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空、角色非法或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	if actor != s.host && !s.isMember(actor) {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	old, ok := s.members[target]
	if !ok {
		return exp, newError(ErrNotInSession, "目标用户不在会话内")
	}
	if actor != s.host && actor != target {
		return exp, newError(ErrPermissionDenied, "只有主持人或本人可以修改角色")
	}
	if old != role {
		if old == RoleVoter {
			s.voters--
			s.box.revoke(target)
		} else {
			s.voters++
		}
		s.members[target] = role
	}
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Start 由主持人开启新议题，进入第一轮投票阶段，轮的开始时刻为
// now。仅允许在无进行中议题时调用；新议题轮数重置、清空全部投票、
// 成员保留。
func (s *Session) Start(user string, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	if user != s.host && !s.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if user != s.host {
		return exp, newError(ErrPermissionDenied, "只有主持人可以开始议题")
	}
	if s.phase != PhaseIdle {
		return exp, newError(ErrInvalidState, "议题尚未结束，不能开始新议题")
	}
	s.phase = PhaseVoting
	s.round = 1
	s.roundStart = now
	s.box.clear()
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Vote 由投票者在投票阶段投下或改投一张牌。可能触发自动揭示。
func (s *Session) Vote(user string, card Card, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !s.deck.Contains(card) || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空、牌不在牌组内或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	role, ok := s.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if role != RoleVoter {
		return exp, newError(ErrPermissionDenied, "观察者不能投票")
	}
	if s.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "非投票阶段不能投票")
	}
	s.box.cast(user, card)
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Unvote 由投票者在投票阶段撤回自己的票。无票时为空操作。
func (s *Session) Unvote(user string, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	role, ok := s.members[user]
	if !ok {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if role != RoleVoter {
		return exp, newError(ErrPermissionDenied, "观察者不能撤回投票")
	}
	if s.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "非投票阶段不能撤回投票")
	}
	s.box.revoke(user)
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Reveal 由主持人在投票阶段揭示，需至少有一张已投的牌（含特殊
// 牌），否则报无人投票。揭示时刻为 now。
func (s *Session) Reveal(user string, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	if user != s.host && !s.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if user != s.host {
		return exp, newError(ErrPermissionDenied, "只有主持人可以揭示")
	}
	if s.phase != PhaseVoting {
		return exp, newError(ErrInvalidState, "非投票阶段不能揭示")
	}
	if s.box.votedCount() == 0 {
		return exp, newError(ErrNoVotes, "无人投票")
	}
	out := s.revealLocked(now, TriggerManual)
	s.lastNow = now
	return out, nil
}

// Revote 由主持人在分歧或无有效票且已用轮数小于 R 时开始新一轮，
// 清空全部投票，新一轮开始时刻为 now。
func (s *Session) Revote(user string, now int64) (*RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == "" || !validNow(now) {
		return nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	exp := s.expireIfDue(now)
	if user != s.host && !s.isMember(user) {
		return exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if user != s.host {
		return exp, newError(ErrPermissionDenied, "只有主持人可以发起新一轮")
	}
	if s.phase != PhaseRevealed {
		return exp, newError(ErrInvalidState, "当前状态不能开始新一轮")
	}
	s.round++
	s.phase = PhaseVoting
	s.roundStart = now
	s.box.clear()
	out := exp
	if auto := s.autoRevealLocked(now); auto != nil {
		out = auto
	}
	s.lastNow = now
	return out, nil
}

// Peek 返回本人的投票与全体已投人数，不返回他人的牌。作为查询，
// 它同样先做到期处理，但不推进会话时钟。
func (s *Session) Peek(user string, now int64) (PeekView, *RevealOutcome, *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var view PeekView
	if user == "" || !validNow(now) {
		return view, nil, newError(ErrInvalidParam, "用户为空或 now 越界")
	}
	if err := s.checkClock(now); err != nil {
		return view, nil, err
	}
	exp := s.expireIfDue(now)
	if !s.isMember(user) {
		return view, exp, newError(ErrNotInSession, "调用者不在会话内")
	}
	if card, ok := s.box.votes[user]; ok {
		view.HasVote = true
		view.Self = card
	}
	view.VotedCount = s.box.votedCount()
	return view, exp, nil
}

// Status 返回会话的可观测状态快照，不含任何他人的牌。
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Phase:      s.phase,
		Round:      s.round,
		RoundStart: s.roundStart,
		Voters:     s.voters,
		Voted:      s.box.votedCount(),
		LastNow:    s.lastNow,
	}
	if s.phase == PhaseVoting {
		st.Deadline = s.roundStart + s.roundTTL
	}
	return st
}
