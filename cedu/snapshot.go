package cedu

// cycleSnapshot 构造周期 k 的计入明细。
//   - phase: 0=周期进行中（只认 win）；1=宽限期中（win 不足时计入候选）；
//     2=已终结（按终结结果）。
func (s *Service) cycleSnapshot(h *holder, k int, phase int) CycleStatus {
	start, end := s.boundsOf(h, k)
	a := h.agg(k)
	r := s.winRaw(h, k)
	carryIn := 0
	if k > 0 {
		if c, ok := h.cache[k-1]; ok && c.outcome == ocPass {
			carryIn = c.carryOut
		}
	}
	if phase == 1 {
		r.requiredRaw += a.zon[Required]
		r.electiveRaw += a.zon[Elective]
		r.onlineRaw += a.zon[Online]
	}
	c := countCredit(r, s.cfg)
	out := CycleStatus{
		Index: k, Start: start, End: end, GraceEnd: s.graceEnd(h, k),
		RequiredIn: c.requiredIn, ElectiveIn: c.electiveIn, OnlineIn: c.onlineIn,
		TotalIn: c.totalIn, CarryIn: carryIn,
	}
	cached, ok := h.cache[k]
	switch {
	case ok && cached.outcome == ocPass:
		out.Met = true
		out.CarryOut = cached.carryOut
	case ok && cached.outcome == ocGracePass:
		out.Met = true
		out.CarryOut = 0
	case ok && cached.outcome == ocExpired:
		out.Met = false
	default:
		if phase == 0 {
			// 周期尚未结束：只按 win 口径判定，不提前续期。
			out.Met = cached.winMet
			out.InGrace = false
		} else {
			// 宽限期中：win 已判定不足，按候选补修后的口径实时判定。
			out.Met = c.met(s.cfg)
			out.InGrace = true
		}
	}
	return out
}

func (s *Service) buildStatus(h *holder, now int) HolderStatus {
	st := HolderStatus{
		HolderID: h.id, IssueDate: h.issueDate, Active: !h.expired,
		Expired: h.expired, AsOf: now,
	}
	k := h.front
	if h.expired {
		k = maxKey(h.cache)
		st.CurrentCycle = s.cycleSnapshot(h, k, 2)
		return st
	}
	_, end := s.boundsOf(h, k)
	ge := s.graceEnd(h, k)
	phase := 0
	if now >= end {
		phase = 1
		if h.cache[k].winMet || now >= ge {
			phase = 2
		}
	}
	st.CurrentCycle = s.cycleSnapshot(h, k, phase)
	return st
}

func maxKey(m map[int]cached) int {
	best := 0
	for k := range m {
		if k > best {
			best = k
		}
	}
	return best
}

// Status 返回 now 时刻的核算结果；now 不得早于上一次被接受操作。
func (s *Service) Status(holderID string, now int) (HolderStatus, error) {
	if !nonempty(holderID) || now < 0 {
		return HolderStatus{}, newError(ErrInvalidParam, "invalid status parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.holders[holderID]
	if h == nil {
		return HolderStatus{}, newError(ErrNotFound, "holder %q not found", holderID)
	}
	if !s.clockOK(now) {
		return HolderStatus{}, newError(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	s.advance(h, now)
	// 查询不推进时钟；但周期到点与证书失效是客观终局，可在此物化。
	return s.buildStatus(h, now), nil
}

// HistoryAt 返回历史时刻 asOf 的核算结果。它通过在独立实例上
// 重放截至 asOf 的事实日志实现，与当时的实时结果一致，且不影响
// 当前状态与时钟。asOf 不得晚于最近一次被接受操作的时刻。
func (s *Service) HistoryAt(holderID string, asOf int) (HolderStatus, error) {
	if !nonempty(holderID) || asOf < 0 {
		return HolderStatus{}, newError(ErrInvalidParam, "invalid history parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.holders[holderID]
	if h == nil {
		return HolderStatus{}, newError(ErrNotFound, "holder %q not found", holderID)
	}
	if asOf > s.lastNow {
		return HolderStatus{}, newError(ErrClockRollback, "history as-of %d later than last accepted now %d", asOf, s.lastNow)
	}
	return replay(s.cfg, h.events, asOf)
}

// Cycle 返回已终结或当前周期 index 在 now 时刻的核算明细，
// 便于核验历史周期的结转与达标构成。纯查询，不改变时钟与状态。
func (s *Service) Cycle(holderID string, index, now int) (CycleStatus, error) {
	if !nonempty(holderID) || now < 0 || index < 0 {
		return CycleStatus{}, newError(ErrInvalidParam, "invalid cycle query parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockOK(now) {
		return CycleStatus{}, newError(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	h := s.holders[holderID]
	if h == nil {
		return CycleStatus{}, newError(ErrNotFound, "holder %q not found", holderID)
	}
	s.advance(h, now)
	if index > h.front && !h.expired {
		return CycleStatus{}, newError(ErrNotFound, "cycle %d not reached yet", index)
	}
	if index > h.front {
		return CycleStatus{}, newError(ErrNotFound, "cycle %d never started", index)
	}
	phase := 2
	if index == h.front && !h.expired {
		_, end := s.boundsOf(h, index)
		ge := s.graceEnd(h, index)
		switch {
		case now < end:
			phase = 0
		case h.cache[index].winMet || now >= ge:
			phase = 2
		default:
			phase = 1
		}
	}
	cs := s.cycleSnapshot(h, index, phase)
	return cs, nil
}
