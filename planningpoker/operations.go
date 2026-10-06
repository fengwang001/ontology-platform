package planningpoker

// Join 让 user 以指定角色加入会话；已在会话内报 ErrAlreadyExists。
// 通过参数/时钟检查后先做到期处理；若因此进入自动揭示则返回揭示结果。
func (s *Session) Join(user string, role Role, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" || (role != RoleVoter && role != RoleObserver) {
		return nil, ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	if _, ok := s.members[user]; ok {
		return nil, ErrAlreadyExists
	}

	s.nextGen++
	s.members[user] = &memberState{role: role, voteSlot: -1, gen: s.nextGen}
	if role == RoleVoter {
		// 在室投票者计数在全部阶段保持准确：Idle 阶段加入的投票者
		// 也计入，Start 时无需特殊处理；揭示后加入不影响已揭示结果。
		s.votersPresent++
	}
	s.accept(now)
	return s.maybeAutoReveal(now), nil
}

// Leave 让成员离开会话。投票阶段离开的投票者其票被撤销，且不再计入
// 全体；若该离开使“全体在室投票者均已投”成立，立即自动揭示。
func (s *Session) Leave(user string, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" {
		return nil, ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	m, ok := s.members[user]
	if !ok {
		return nil, ErrNotInSession
	}

	if m.role == RoleVoter {
		s.votersPresent--
	}
	if m.voteSlot >= 0 {
		// 投票阶段离开：撤销其票并更新槽位。
		if s.phase == PhaseVoting {
			delete(s.votedSet, user)
			s.slotCounts[m.voteSlot]--
			s.votedVoters--
		} else if g, ok := s.revealedGen[user]; ok && g == m.gen {
			// 揭示后离开：席位在揭示快照中才递减在室计数（O(1)）。
			s.revealedPresent--
		}
		m.voteSlot = -1
	}
	delete(s.members, user)
	s.accept(now)
	return s.maybeAutoReveal(now), nil
}

// SetRole 修改成员角色。投票阶段投票者转观察者时撤销其票；两种方向
// 都立即更新“全体”。揭示后改角色不影响已揭示结果。
func (s *Session) SetRole(user string, role Role, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" || (role != RoleVoter && role != RoleObserver) {
		return nil, ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	m, ok := s.members[user]
	if !ok {
		return nil, ErrNotInSession
	}

	if m.role != role {
		switch {
		case m.role == RoleVoter && role == RoleObserver:
			s.votersPresent--
			if m.voteSlot >= 0 {
				if s.phase == PhaseVoting {
					delete(s.votedSet, user)
					s.slotCounts[m.voteSlot]--
					s.votedVoters--
				} else {
					if g, ok := s.revealedGen[user]; ok && g == m.gen {
						s.revealedPresent--
					}
				}
				m.voteSlot = -1
			}
		case m.role == RoleObserver && role == RoleVoter:
			s.votersPresent++
		}
		m.role = role
	}
	s.accept(now)
	return s.maybeAutoReveal(now), nil
}

// Start 仅主持人可调用，开启一个新议题并进入第一轮投票。
// 仅在无进行中议题（Idle）或上一议题已结束（Ended）时允许。
func (s *Session) Start(user string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return err
	}
	// 主持人身份独立于成员表：主持人无需 Join 即拥有主持人权限。
	if user != s.host {
		if _, ok := s.members[user]; !ok {
			return ErrNotInSession
		}
		return ErrForbidden
	}
	if s.phase != PhaseIdle && s.phase != PhaseEnded {
		return ErrState
	}

	// 新议题：轮数重置，成员保留；清掉上一议题的票与结果。
	s.clearVotes()
	s.votersPresent = 0
	for _, m := range s.members {
		if m.role == RoleVoter {
			s.votersPresent++
		}
	}
	s.round = 1
	s.roundStart = now
	s.phase = PhaseVoting
	s.lastResult = nil
	s.accept(now)
	// 自动揭示只在“存在已投票”时才有意义；新一轮无票，这里不会触发。
	s.maybeAutoReveal(now)
	return nil
}

// Vote 由投票者在投票阶段投下一张牌（可改投）。牌必须属于本牌组。
func (s *Session) Vote(user string, card Card, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deck.indexOf(card) == -2 {
		return nil, ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	m, ok := s.members[user]
	if !ok {
		return nil, ErrNotInSession
	}
	if m.role != RoleVoter {
		return nil, ErrForbidden
	}
	if s.phase != PhaseVoting {
		return nil, ErrState
	}

	slot := s.cardSlot(card)
	if m.voteSlot >= 0 {
		s.slotCounts[m.voteSlot]-- // 改投：不改变 votedVoters
	} else {
		s.votedVoters++
		s.votedSet[user] = m.gen
	}
	s.slotCounts[slot]++
	m.voteSlot = slot
	s.accept(now)
	return s.maybeAutoReveal(now), nil
}

// Unvote 撤回本人的票。从未投票时撤回为幂等成功。
func (s *Session) Unvote(user string, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	m, ok := s.members[user]
	if !ok {
		return nil, ErrNotInSession
	}
	if m.role != RoleVoter {
		return nil, ErrForbidden
	}
	if s.phase != PhaseVoting {
		return nil, ErrState
	}

	if m.voteSlot >= 0 {
		s.slotCounts[m.voteSlot]--
		m.voteSlot = -1
		s.votedVoters--
		delete(s.votedSet, user)
	}
	s.accept(now)
	return nil, nil
}

// Reveal 仅主持人可调用，手动揭示当前轮。至少需一张已投牌（含特殊牌）。
func (s *Session) Reveal(user string, now int64) (*RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" {
		return nil, ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	if user != s.host {
		if _, ok := s.members[user]; !ok {
			return nil, ErrNotInSession
		}
		return nil, ErrForbidden
	}
	if s.phase != PhaseVoting {
		return nil, ErrState
	}
	if s.votedVoters == 0 {
		return nil, ErrNoVotes
	}

	s.accept(now)
	s.doReveal(now)
	return s.lastResult, nil
}

// Revote 仅主持人可调用：分歧或无有效票且轮数未用尽时开启新一轮。
func (s *Session) Revote(user string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if user == "" {
		return ErrInvalidArgument
	}
	if err := s.checkClockAndExpire(now); err != nil {
		return err
	}
	if user != s.host {
		if _, ok := s.members[user]; !ok {
			return ErrNotInSession
		}
		return ErrForbidden
	}
	if s.phase != PhaseRevealed {
		return ErrState
	}
	cat := s.lastResult.Category
	if cat != ResultDiverged && cat != ResultNoValidVotes {
		return ErrState
	}
	if s.round >= s.roundLimit {
		return ErrState
	}

	s.clearVotes()
	s.round++
	s.roundStart = now
	s.phase = PhaseVoting
	s.lastResult = nil
	s.accept(now)
	return nil
}

// Peek 返回视图：揭示前只返回本人投票与已投人数，绝不返回他人的牌；
// 揭示后（含到期惰性揭示）返回完整结果。
func (s *Session) Peek(user string, now int64) (*PeekView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClockAndExpire(now); err != nil {
		return nil, err
	}
	if _, ok := s.members[user]; !ok {
		return nil, ErrNotInSession
	}

	v := &PeekView{
		Phase:      s.phase,
		Round:      s.round,
		VoterTotal: s.votersPresent,
	}
	if s.phase == PhaseVoting {
		v.VotedCount = s.votedVoters
		m := s.members[user]
		if m.role == RoleVoter && m.voteSlot >= 0 {
			card := s.slotCard(m.voteSlot)
			v.OwnVote = &card
		}
	} else {
		v.VotedCount = s.presentVotedCount()
		v.Outcome = s.lastResult
	}
	s.accept(now)
	return v, nil
}

// Host 返回会话主持人 ID。
func (s *Session) Host() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host
}

// cardSlot 返回牌对应的槽位下标。调用前须保证牌属于牌组。
func (s *Session) cardSlot(c Card) int {
	idx := s.deck.indexOf(c)
	if idx >= 0 {
		return idx
	}
	// 特殊牌：uncertain(idx=-1) -> 数值牌数量；break(idx=-2) -> 其后一位。
	return s.deck.numericCount() + specialSlot(c)
}

// slotCard 是 cardSlot 的逆映射。
func (s *Session) slotCard(slot int) Card {
	n := s.deck.numericCount()
	if slot < n {
		return s.deck.numeric[slot]
	}
	return specialCards[slot-n]
}
