package blocksched

// capFor 返回对端当前有效的并发上限：max(1, K - floor(timeouts/2))。
// 调用方必须持有 s.mu。
func (s *Scheduler) capFor(p *peer) int {
	c := s.k - p.timeouts/2
	if c < 1 {
		c = 1
	}
	return c
}

// Next 决定对端 id 下一个应请求的块。
// 错误优先级：ErrClock > ErrNoPeer > ErrBanned。
// 无块可发时返回 (0, false, nil)，且不改变任何状态。
func (s *Scheduler) Next(now int64, id string) (block int, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return 0, false, ErrClock
	}
	p, exists := s.peers[id]
	if !exists {
		return 0, false, ErrNoPeer
	}
	if p.banned {
		return 0, false, ErrBanned
	}

	s.now = now

	if s.completeAllLocked() {
		return 0, false, nil
	}
	if len(p.inflight) >= s.capFor(p) || s.totalFlight >= s.g {
		return 0, false, nil
	}

	// 是否存在“新块”：未完成、全系统当前无任何在途请求、avail>0。
	anyNewBlock := false
	for b := 0; b < s.b; b++ {
		if !s.complete[b] && s.blockFlight[b] == 0 && s.availCount[b] > 0 {
			anyNewBlock = true
			break
		}
	}

	if anyNewBlock {
		// 新块候选：未完成、对端拥有、全系统无在途、该对端无失败记录。
		// 选择键：(avail 最小, 块号最小)。
		best := -1
		bestAvail := 0
		for b := 0; b < s.b; b++ {
			if s.complete[b] || !p.have[b] || s.blockFlight[b] != 0 {
				continue
			}
			if _, failed := p.fails[b]; failed {
				continue
			}
			a := s.availCount[b]
			if a == 0 {
				continue
			}
			if best == -1 || a < bestAvail || (a == bestAvail && b < best) {
				best = b
				bestAvail = a
			}
		}
		// 存在新块但该对端没有可请求的新块：即使可重复请求也返回空。
		if best == -1 {
			return 0, false, nil
		}
		s.issue(p, best, now)
		return best, true, nil
	}

	// 收尾期：允许重复请求。候选键：(该块在途数最小, avail 最小, 块号最小)。
	best := -1
	bestFlight := 0
	bestAvail := 0
	for b := 0; b < s.b; b++ {
		if s.complete[b] || !p.have[b] {
			continue
		}
		if _, mine := p.inflight[b]; mine {
			continue
		}
		if s.blockFlight[b] >= s.m {
			continue
		}
		if _, failed := p.fails[b]; failed {
			continue
		}
		a := s.availCount[b]
		fl := s.blockFlight[b]
		if best == -1 || fl < bestFlight || (fl == bestFlight && a < bestAvail) ||
			(fl == bestFlight && a == bestAvail && b < best) {
			best = b
			bestFlight = fl
			bestAvail = a
		}
	}
	if best == -1 {
		return 0, false, nil
	}
	s.issue(p, best, now)
	return best, true, nil
}

// issue 登记一条新的在途请求。调用方必须持有 s.mu。
func (s *Scheduler) issue(p *peer, b int, now int64) {
	req := &request{issued: now}
	p.inflight[b] = req
	s.inflight[inflightKey{id: p.id, b: b}] = req
	s.blockFlight[b]++
	s.totalFlight++
}

// completeAllLocked 报告是否全部块完成。调用方必须持有 s.mu。
func (s *Scheduler) completeAllLocked() bool {
	for b := 0; b < s.b; b++ {
		if !s.complete[b] {
			return false
		}
	}
	return true
}
