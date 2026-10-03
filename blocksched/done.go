package blocksched

import "sort"

// Done 回报 (id, b) 上某条在途请求的校验结果。
// 错误优先级：ErrClock > ErrNoPeer > ErrBadArg > ErrNoRequest。
// 成功时返回该块上其余被取消请求的对端 id（升序）；
// 失败且 fails 达到 F 时返回 banned=true。
func (s *Scheduler) Done(now int64, id string, b int, success bool) (canceled []string, banned bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return nil, false, ErrClock
	}
	p, ok := s.peers[id]
	if !ok {
		return nil, false, ErrNoPeer
	}
	if b < 0 || b >= s.b {
		return nil, false, ErrBadArg
	}
	if _, inflight := p.inflight[b]; !inflight {
		return nil, false, ErrNoRequest
	}

	s.now = now
	s.removeRequest(id, b)

	if success {
		if !s.complete[b] {
			s.complete[b] = true
		}
		// 取消该块上其余所有对端的在途请求。
		for _, other := range s.peers {
			if other.id == id {
				continue
			}
			if _, inflight := other.inflight[b]; inflight {
				canceled = append(canceled, other.id)
				s.removeRequest(other.id, b)
			}
		}
		sort.Strings(canceled)
		p.timeouts = 0
		return canceled, false, nil
	}

	// 校验失败：永不再向该对端请求块 b。
	p.fails[b] = struct{}{}
	if len(p.fails) >= s.f {
		s.banPeerLocked(p)
		return nil, true, nil
	}
	return nil, false, nil
}

// banPeerLocked 封禁对端：移除其全部在途请求并将其从 avail 中剔除。
// 调用方必须持有 s.mu。
func (s *Scheduler) banPeerLocked(p *peer) {
	p.banned = true
	s.banned[p.id] = struct{}{}
	for b := range p.inflight {
		s.removeRequest(p.id, b)
	}
	for b := 0; b < s.b; b++ {
		if p.have[b] {
			s.availCount[b]--
		}
	}
}
