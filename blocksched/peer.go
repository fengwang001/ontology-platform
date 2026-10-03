package blocksched

// avail 返回块 b 当前可服务的对端数：未被封禁、未被删除且拥有该块。
// 调用方必须持有 s.mu。
func (s *Scheduler) avail(b int) int {
	return s.availCount[b]
}

// removeRequest 移除一条在途请求并维护计数。调用方必须持有 s.mu。
func (s *Scheduler) removeRequest(id string, b int) bool {
	key := inflightKey{id: id, b: b}
	if _, ok := s.inflight[key]; !ok {
		return false
	}
	delete(s.inflight, key)
	delete(s.peers[id].inflight, b)
	s.blockFlight[b]--
	s.totalFlight--
	return true
}

// AddPeer 添加一个对端。
// 错误优先级：ErrBadArg（id 空或 have 长度不对）> ErrPeerExists > ErrBanned。
func (s *Scheduler) AddPeer(id string, have []bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || len(have) != s.b {
		return ErrBadArg
	}
	if _, ok := s.peers[id]; ok {
		return ErrPeerExists
	}
	if _, ok := s.banned[id]; ok {
		return ErrBanned
	}

	copied := make([]bool, s.b)
	copy(copied, have)
	s.peers[id] = &peer{
		id:       id,
		have:     copied,
		fails:    make(map[int]struct{}),
		inflight: make(map[int]*request),
	}
	for b := range copied {
		if copied[b] {
			s.availCount[b]++
		}
	}
	return nil
}

// Have 单调置位对端 id 对块 b 的拥有关系。
// 对已封禁但未 Drop 的对端仍视为存在：允许置位，但不计入 avail。
func (s *Scheduler) Have(id string, b int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[id]
	if !ok {
		return ErrNoPeer
	}
	if b < 0 || b >= s.b {
		return ErrBadArg
	}
	if !p.have[b] {
		p.have[b] = true
		if !p.banned {
			s.availCount[b]++
		}
	}
	return nil
}

// Drop 取消该对端全部在途请求并删除其记录。
// 封禁记录始终保留：被封禁 id 之后永远无法再次 AddPeer。
func (s *Scheduler) Drop(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[id]
	if !ok {
		return ErrNoPeer
	}

	for b := range p.inflight {
		s.removeRequest(id, b)
	}

	if !p.banned {
		for b := 0; b < s.b; b++ {
			if p.have[b] {
				s.availCount[b]--
			}
		}
	}
	if p.banned {
		s.banned[id] = struct{}{}
	}
	delete(s.peers, id)
	return nil
}
