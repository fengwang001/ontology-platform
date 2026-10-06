package planningpoker

// checkClockAndExpire 执行所有入口共用的前置逻辑：
// 时钟回退检查，以及（通过时钟检查后）在执行操作本身之前先做惰性到期。
// 返回 false 表示调用方应直接返回时钟错误，且不得改变任何状态。
// 到期揭示本身是被接受的状态推进，即使随后操作被拒绝也已生效。
func (s *Session) checkClockAndExpire(now int64) error {
	if now < s.lastNow {
		return ErrClockRewind
	}
	s.processExpire(now)
	return nil
}

// accept 标记一次被接受操作（含查询）的时间戳。
func (s *Session) accept(now int64) {
	s.lastNow = now
}

// processExpiry 惰性处理当前轮到期：恰等于开始时刻+T 即视为到期。
// 已到期则在到期时刻揭示；到期时刻无人投票按无有效票处理。
func (s *Session) processExpire(now int64) {
	if s.phase != PhaseVoting {
		return
	}
	deadline := s.roundStart + s.roundLength
	if now < deadline {
		return
	}
	s.doReveal(deadline)
}

// maybeAutoReveal 在自动揭示开启时，检查“在室投票者 >= 1 且全体已投”，
// 成立则在 now 立即揭示。该判定只读取两个整数计数器，开销 O(1)。
func (s *Session) maybeAutoReveal(now int64) *RevealResult {
	if s.phase != PhaseVoting || !s.autoReveal {
		return nil
	}
	if s.votersPresent >= 1 && s.votedVoters == s.votersPresent {
		s.doReveal(now)
		return s.lastResult
	}
	return nil
}

// doReveal 在当前轮执行一次揭示：统计数值票、判定结果类别并推进阶段。
// 每轮至多调用一次（调用方负责保证）。
func (s *Session) doReveal(at int64) *RevealResult {
	n := s.deck.numericCount()
	// votedSet 已由各操作 O(1) 增量维护，此处直接交换引用完成“冻结”：
	// 快照拿走当前 map，活跃集合换成新空 map。整个揭示不遍历任何成员，
	// 因而揭示开销只与牌组大小有关。
	s.revealedGen = s.votedSet
	s.revealedPresent = len(s.revealedGen)
	s.votedSet = make(map[string]int)

	// 统计：只遍历牌组大小的 slotCounts，与参与者总数无关。
	first, last := -1, -1
	numericVotes := 0
	for k := 0; k < n; k++ {
		if s.slotCounts[k] > 0 {
			if first == -1 {
				first = k
			}
			last = k
			numericVotes += s.slotCounts[k]
		}
	}

	res := &RevealResult{
		Round:        s.round,
		RevealedAt:   at,
		Distribution: s.buildDistribution(),
		NumericVotes: numericVotes,
	}

	finalRound := s.round >= s.roundLimit

	switch {
	case numericVotes == 0:
		if finalRound {
			res.Category = ResultTerminalNoResult
			res.Final = true
			s.phase = PhaseEnded
		} else {
			res.Category = ResultNoValidVotes
			res.Final = false
			s.phase = PhaseRevealed
		}
	case first == last:
		res.Category = ResultConsensus
		res.Value = s.deck.numeric[first].Value
		res.ValueIsMeaning = true
		res.Final = true
		s.phase = PhaseEnded
	case last-first == 1:
		// 最大与最小数值牌在牌组中位置相邻（相差恰为一位），取较大者。
		res.Category = ResultConverged
		res.Value = s.deck.numeric[last].Value
		res.ValueIsMeaning = true
		res.Final = true
		s.phase = PhaseEnded
	case !finalRound:
		res.Category = ResultDiverged
		res.Final = false
		s.phase = PhaseRevealed
	default:
		// 分歧且轮次用尽：按牌组位置排序的下中位（偶数张取靠前一张）。
		res.Category = ResultForcedValue
		res.Value = s.lowerMedianValue(n, numericVotes)
		res.ValueIsMeaning = true
		res.Final = true
		s.phase = PhaseEnded
	}

	s.lastResult = res
	return res
}

// lowerMedianValue 返回数值票按牌组位置排序后的下中位。
// m 张票时下中位为排序后第 ceil(m/2) 张，即下标 target=(m-1)/2（0 起）。
func (s *Session) lowerMedianValue(n, m int) int {
	target := (m - 1) / 2
	seen := 0
	for k := 0; k < n; k++ {
		c := s.slotCounts[k]
		if seen+c > target {
			return s.deck.numeric[k].Value
		}
		seen += c
	}
	return 0 // 不可达：调用方保证 m>=1
}

// buildDistribution 按固定牌序输出每张牌的得票（含 0 票牌），
// 保证重放结果逐字节一致；只遍历牌组，不遍历成员。
func (s *Session) buildDistribution() []CardCount {
	cards := s.deck.allCards()
	out := make([]CardCount, len(cards))
	for i, c := range cards {
		out[i] = CardCount{Card: c, Count: s.slotCounts[i]}
	}
	return out
}

// clearVotes 清空一轮的全部投票与槽位计数，进入下一轮。
func (s *Session) clearVotes() {
	for _, m := range s.members {
		m.voteSlot = -1
	}
	for i := range s.slotCounts {
		s.slotCounts[i] = 0
	}
	s.votedVoters = 0
	s.revealedPresent = 0
	s.votedSet = make(map[string]int)
	s.revealedGen = nil
}

// presentVotedCount 返回揭示快照中目前仍在室、且席位代次匹配的人数。
// 揭示后同名离开再加入（新代次）不会被误计；计数随离开/改角色 O(1) 递减。
func (s *Session) presentVotedCount() int { return s.revealedPresent }
