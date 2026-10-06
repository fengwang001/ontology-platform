package station

// advance 将时钟推进到 t，按充满时刻切分并逐台结算。
// 复杂度只与在站车辆数及区间内充满次数（和定时上限变更数）有关，
// 通过直接跳跃到下一个事件时刻避免逐秒循环。
func (s *Station) advance(t int) error {
	if t < s.now {
		return ErrClockBack
	}
	for {
		dt, hasFull := s.nextStep(t)
		if dt == 0 && !hasFull {
			break
		}
		if dt > 0 {
			for _, se := range s.sessions {
				if se.State == StateCharging {
					se.accumulate(dt)
				}
			}
			s.now += dt
		}
		if hasFull {
			s.settleFull()
		}
		if len(s.pending) > 0 && s.pending[0].at <= s.now {
			s.applyDueCaps()
		}
	}
	return nil
}

// nextStep 返回下一次跳跃的秒数，以及跳跃后是否有车恰好充满。
// 跳跃目标取三者最早：某车充满、下一个上限变更生效、推进终点。
// 充满时刻与上限变更同时刻时，先结算充满（hasFull=true）。
func (s *Station) nextStep(limit int) (int, bool) {
	step := limit - s.now
	if step < 0 {
		step = 0
	}
	fullAt := -1
	for _, se := range s.sessions {
		if se.State != StateCharging || se.Pwr <= 0 || se.remaining <= 0 {
			continue
		}
		dt := ceilDiv(se.remaining, se.Pwr)
		if fullAt < 0 || dt < fullAt {
			fullAt = dt
		}
	}
	capAt := -1
	if len(s.pending) > 0 {
		capAt = s.pending[0].at - s.now
	}
	best := step
	if fullAt >= 0 && fullAt < best {
		best = fullAt
	}
	if capAt >= 0 && capAt < best {
		best = capAt
	}
	return best, fullAt == best
}

// settleFull 结算当前时刻恰好充满的车辆：同时刻按插枪先后依次处理，
// 每台标记充满后立即重新分配（后续同时刻车辆按新功率判定）。
func (s *Station) settleFull() {
	for {
		var victim *sess
		for _, se := range s.sessions {
			if se.State != StateCharging || se.remaining > 0 {
				continue
			}
			if victim == nil || se.PlugOrder < victim.PlugOrder {
				victim = se
			}
		}
		if victim == nil {
			return
		}
		victim.State = StateFull
		victim.Pwr = 0
		s.reallocate(causeOther)
	}
}

// applyDueCaps 生效所有生效时刻不晚于当前时刻的上限变更，
// 按（生效时刻, 登记次序）逐条应用并重新分配。
func (s *Station) applyDueCaps() {
	for len(s.pending) > 0 && s.pending[0].at <= s.now {
		ch := s.pending[0]
		s.pending = s.pending[1:]
		s.cap = ch.cap
		s.reallocate(causeCapDown)
	}
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// accumulate 按当前功率累计 d 秒电量，不超过需求。
func (se *sess) accumulate(d int) {
	add := se.Pwr * d
	if se.remaining >= 0 && add > se.remaining {
		add = se.remaining
	}
	se.remaining -= add
	se.Energy += add
	if se.remaining < 0 {
		se.Energy += se.remaining
		se.remaining = 0
	}
}
